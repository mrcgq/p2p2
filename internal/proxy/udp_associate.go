
package proxy

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/anthropics/phantom-core/internal/logger"
)

// UDPAssociateHandler 处理 SOCKS5 UDP ASSOCIATE
type UDPAssociateHandler struct {
	handler      *Handler
	log          *logger.Logger
	timeout      time.Duration
	associations sync.Map
}

// UDPAssociation UDP 关联
type UDPAssociation struct {
	ID         string
	ClientAddr net.Addr
	UDPConn    *net.UDPConn
	Created    time.Time
	LastActive time.Time
	closed     bool
	mu         sync.Mutex
}

// NewUDPAssociateHandler 创建 UDP ASSOCIATE 处理器
func NewUDPAssociateHandler(handler *Handler, log *logger.Logger, timeout time.Duration) *UDPAssociateHandler {
	h := &UDPAssociateHandler{
		handler: handler,
		log:     log,
		timeout: timeout,
	}

	go h.cleanup()

	return h
}

// CreateAssociation 创建新的 UDP 关联
func (h *UDPAssociateHandler) CreateAssociation(clientAddr net.Addr) (*net.UDPAddr, func(), error) {
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, nil, fmt.Errorf("创建 UDP 监听器失败: %w", err)
	}

	localAddr := udpConn.LocalAddr().(*net.UDPAddr)

	assoc := &UDPAssociation{
		ID:         fmt.Sprintf("%s-%d", clientAddr.String(), time.Now().UnixNano()),
		ClientAddr: clientAddr,
		UDPConn:    udpConn,
		Created:    time.Now(),
		LastActive: time.Now(),
	}

	h.associations.Store(assoc.ID, assoc)

	go h.handleAssociation(assoc)

	cleanup := func() {
		h.closeAssociation(assoc.ID)
	}

	return localAddr, cleanup, nil
}

func (h *UDPAssociateHandler) handleAssociation(assoc *UDPAssociation) {
	buf := make([]byte, 65535)

	for {
		assoc.UDPConn.SetReadDeadline(time.Now().Add(h.timeout))
		n, remoteAddr, err := assoc.UDPConn.ReadFromUDP(buf)
		if err != nil {
			break
		}

		assoc.mu.Lock()
		if assoc.closed {
			assoc.mu.Unlock()
			break
		}
		assoc.LastActive = time.Now()
		assoc.mu.Unlock()

		// 解析 SOCKS5 UDP 请求头
		if n < 10 {
			continue
		}

		if buf[0] != 0 || buf[1] != 0 {
			continue
		}

		frag := buf[2]
		if frag != 0 {
			continue
		}

		atyp := buf[3]
		var targetAddr string
		var offset int

		switch atyp {
		case atypIPv4:
			if n < 10 {
				continue
			}
			ip := net.IP(buf[4:8])
			port := binary.BigEndian.Uint16(buf[8:10])
			targetAddr = fmt.Sprintf("%s:%d", ip.String(), port)
			offset = 10

		case atypIPv6:
			if n < 22 {
				continue
			}
			ip := net.IP(buf[4:20])
			port := binary.BigEndian.Uint16(buf[20:22])
			targetAddr = fmt.Sprintf("[%s]:%d", ip.String(), port)
			offset = 22

		case atypDomain:
			if n < 7 {
				continue
			}
			domainLen := int(buf[4])
			if n < 7+domainLen {
				continue
			}
			domain := string(buf[5 : 5+domainLen])
			port := binary.BigEndian.Uint16(buf[5+domainLen : 7+domainLen])
			targetAddr = fmt.Sprintf("%s:%d", domain, port)
			offset = 7 + domainLen

		default:
			continue
		}

		data := buf[offset:n]

		// 通过隧道发送
		go h.forwardToTarget(assoc, targetAddr, data, remoteAddr)
	}
}

func (h *UDPAssociateHandler) forwardToTarget(assoc *UDPAssociation, targetAddr string, data []byte, clientUDP *net.UDPAddr) {
	// TODO: 实现通过隧道的 UDP 转发
	// 当前简化实现：直接发送
	udpAddr, err := net.ResolveUDPAddr("udp", targetAddr)
	if err != nil {
		return
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return
	}
	defer conn.Close()

	conn.Write(data)

	// 接收响应
	respBuf := make([]byte, 65535)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(respBuf)
	if err != nil {
		return
	}

	// 构建 SOCKS5 UDP 响应
	response := h.buildUDPResponse(targetAddr, respBuf[:n])
	assoc.UDPConn.WriteToUDP(response, clientUDP)
}

func (h *UDPAssociateHandler) buildUDPResponse(targetAddr string, data []byte) []byte {
	host, portStr, _ := net.SplitHostPort(targetAddr)
	port, _ := net.LookupPort("udp", portStr)

	var response []byte
	response = append(response, 0, 0, 0)

	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		response = append(response, atypIPv4)
		response = append(response, ip4...)
	} else if ip6 := ip.To16(); ip6 != nil {
		response = append(response, atypIPv6)
		response = append(response, ip6...)
	} else {
		response = append(response, atypDomain)
		response = append(response, byte(len(host)))
		response = append(response, []byte(host)...)
	}

	portBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(portBuf, uint16(port))
	response = append(response, portBuf...)

	response = append(response, data...)
	return response
}

func (h *UDPAssociateHandler) closeAssociation(id string) {
	if v, ok := h.associations.LoadAndDelete(id); ok {
		assoc := v.(*UDPAssociation)

		assoc.mu.Lock()
		assoc.closed = true
		assoc.mu.Unlock()

		assoc.UDPConn.Close()
	}
}

func (h *UDPAssociateHandler) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		h.associations.Range(func(key, value interface{}) bool {
			assoc := value.(*UDPAssociation)
			assoc.mu.Lock()
			lastActive := assoc.LastActive
			assoc.mu.Unlock()

			if now.Sub(lastActive) > h.timeout {
				h.closeAssociation(key.(string))
			}
			return true
		})
	}
}


