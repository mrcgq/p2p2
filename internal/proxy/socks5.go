
package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/anthropics/phantom-core/internal/logger"
)

const (
	socks5Version = 0x05

	authNone     = 0x00
	authPassword = 0x02
	authNoAccept = 0xFF

	cmdConnect = 0x01
	cmdBind    = 0x02
	cmdUDP     = 0x03

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	repSuccess          = 0x00
	repGeneralFailure   = 0x01
	repNotAllowed       = 0x02
	repNetworkUnreach   = 0x03
	repHostUnreach      = 0x04
	repConnRefused      = 0x05
	repTTLExpired       = 0x06
	repCmdNotSupported  = 0x07
	repAtypNotSupported = 0x08
)

// Socks5Server SOCKS5 代理服务器
type Socks5Server struct {
	addr       string
	udpEnabled bool
	handler    *Handler
	log        *logger.Logger
	listener   net.Listener

	udpHandler *UDPAssociateHandler
}

// NewSocks5Server 创建新的 SOCKS5 服务器
func NewSocks5Server(addr string, udpEnabled bool, handler *Handler, log *logger.Logger) *Socks5Server {
	s := &Socks5Server{
		addr:       addr,
		udpEnabled: udpEnabled,
		handler:    handler,
		log:        log,
	}

	if udpEnabled {
		s.udpHandler = NewUDPAssociateHandler(handler, log, 60*time.Second)
	}

	return s
}

// Start 启动 SOCKS5 服务器
func (s *Socks5Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("监听: %w", err)
	}
	s.listener = listener

	s.log.Info("SOCKS5 服务器监听于 %s (UDP: %v)", s.addr, s.udpEnabled)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				s.log.Debug("接受连接错误: %v", err)
				continue
			}
		}

		go s.handleConn(conn)
	}
}

func (s *Socks5Server) handleConn(conn net.Conn) {
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// 读取版本和认证方法
	buf := make([]byte, 258)
	n, err := conn.Read(buf)
	if err != nil || n < 2 {
		return
	}

	if buf[0] != socks5Version {
		return
	}

	// 选择无认证
	conn.Write([]byte{socks5Version, authNone})

	// 读取请求
	n, err = conn.Read(buf)
	if err != nil || n < 7 {
		return
	}

	if buf[0] != socks5Version {
		return
	}

	cmd := buf[1]

	switch cmd {
	case cmdConnect:
		s.handleConnect(conn, buf[:n])
	case cmdUDP:
		if s.udpEnabled {
			s.handleUDPAssociate(conn, buf[:n])
		} else {
			s.sendReply(conn, repCmdNotSupported, nil)
		}
	default:
		s.sendReply(conn, repCmdNotSupported, nil)
	}
}

func (s *Socks5Server) handleConnect(conn net.Conn, buf []byte) {
	host, port, addrEnd, err := s.parseAddress(buf)
	if err != nil {
		s.sendReply(conn, repAtypNotSupported, nil)
		return
	}

	s.log.Debug("SOCKS5 CONNECT: %s:%d", host, port)

	// 发送成功回复
	reply := make([]byte, addrEnd)
	reply[0] = socks5Version
	reply[1] = repSuccess
	reply[2] = 0x00
	copy(reply[3:], buf[3:addrEnd])
	conn.Write(reply)

	// 取消超时
	conn.SetDeadline(time.Time{})

	// 处理连接
	s.handler.HandleConn("tcp", host, port, conn)
}

func (s *Socks5Server) handleUDPAssociate(conn net.Conn, buf []byte) {
	if s.udpHandler == nil {
		s.sendReply(conn, repCmdNotSupported, nil)
		return
	}

	// 创建 UDP 关联
	udpAddr, cleanup, err := s.udpHandler.CreateAssociation(conn.RemoteAddr())
	if err != nil {
		s.sendReply(conn, repGeneralFailure, nil)
		return
	}

	// 发送 UDP 监听地址
	s.sendReply(conn, repSuccess, udpAddr)

	s.log.Debug("SOCKS5 UDP ASSOCIATE: %v", udpAddr)

	// 取消超时
	conn.SetDeadline(time.Time{})

	// 等待 TCP 连接关闭
	buf2 := make([]byte, 1)
	conn.Read(buf2)

	cleanup()
}

func (s *Socks5Server) parseAddress(buf []byte) (string, uint16, int, error) {
	if len(buf) < 7 {
		return "", 0, 0, fmt.Errorf("缓冲区过短")
	}

	atyp := buf[3]
	var host string
	var port uint16
	var addrEnd int

	switch atyp {
	case atypIPv4:
		if len(buf) < 10 {
			return "", 0, 0, fmt.Errorf("IPv4 地址无效")
		}
		host = net.IP(buf[4:8]).String()
		port = binary.BigEndian.Uint16(buf[8:10])
		addrEnd = 10

	case atypDomain:
		domainLen := int(buf[4])
		if len(buf) < 5+domainLen+2 {
			return "", 0, 0, fmt.Errorf("域名无效")
		}
		host = string(buf[5 : 5+domainLen])
		port = binary.BigEndian.Uint16(buf[5+domainLen : 7+domainLen])
		addrEnd = 7 + domainLen

	case atypIPv6:
		if len(buf) < 22 {
			return "", 0, 0, fmt.Errorf("IPv6 地址无效")
		}
		host = net.IP(buf[4:20]).String()
		port = binary.BigEndian.Uint16(buf[20:22])
		addrEnd = 22

	default:
		return "", 0, 0, fmt.Errorf("不支持的地址类型")
	}

	return host, port, addrEnd, nil
}

func (s *Socks5Server) sendReply(conn net.Conn, rep byte, addr net.Addr) {
	reply := []byte{socks5Version, rep, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}

	if addr != nil {
		switch a := addr.(type) {
		case *net.UDPAddr:
			if ip4 := a.IP.To4(); ip4 != nil {
				reply[3] = atypIPv4
				copy(reply[4:8], ip4)
				binary.BigEndian.PutUint16(reply[8:10], uint16(a.Port))
			} else {
				reply = []byte{socks5Version, rep, 0x00, atypIPv6}
				reply = append(reply, a.IP.To16()...)
				portBuf := make([]byte, 2)
				binary.BigEndian.PutUint16(portBuf, uint16(a.Port))
				reply = append(reply, portBuf...)
			}
		}
	}

	conn.Write(reply)
}

// Stop 停止服务器
func (s *Socks5Server) Stop() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

