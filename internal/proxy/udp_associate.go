// internal/proxy/udp_associate.go

package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
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
	ClientUDP  *net.UDPAddr
	UDPConn    *net.UDPConn
	Created    time.Time
	LastActive time.Time
	sessions   sync.Map // targetAddr -> *UDPSession
	closed     bool
	mu         sync.Mutex
}

// UDPSession 管理到特定目标的 UDP 会话
type UDPSession struct {
	TargetAddr string
	Stream     io.ReadWriteCloser
	LastActive time.Time
	mu         sync.Mutex
}

// UDPDatagram 封装 UDP 数据报用于隧道传输
type UDPDatagram struct {
	Data []byte
}

// WriteTo 将数据报写入流
func (d *UDPDatagram) WriteTo(w io.Writer) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(d.Data)))

	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(d.Data)
	return err
}

// ReadFrom 从流读取数据报
func (d *UDPDatagram) ReadFrom(r io.Reader) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return err
	}

	length := binary.BigEndian.Uint32(header)
	if length > 65535 {
		return fmt.Errorf("UDP datagram too large: %d", length)
	}

	d.Data = make([]byte, length)
	_, err := io.ReadFull(r, d.Data)
	return err
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
		// 记录客户端 UDP 地址
		if assoc.ClientUDP == nil {
			assoc.ClientUDP = remoteAddr
		}
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

		data := make([]byte, n-offset)
		copy(data, buf[offset:n])

		// 通过隧道发送
		go h.forwardToTarget(assoc, targetAddr, data, remoteAddr)
	}
}

// forwardToTarget 通过隧道转发 UDP 数据到目标
func (h *UDPAssociateHandler) forwardToTarget(
	assoc *UDPAssociation,
	targetAddr string,
	data []byte,
	clientUDP *net.UDPAddr,
) {
	// 1. 解析目标地址
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		h.log.Debug("解析目标地址失败: addr=%s, error=%v", targetAddr, err)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		h.log.Debug("解析端口失败: port=%s, error=%v", portStr, err)
		return
	}

	// 2. 获取或创建 UDP 会话
	session, err := h.getOrCreateSession(assoc, targetAddr, host, uint16(port), clientUDP)
	if err != nil {
		h.log.Error("创建 UDP 会话失败: target=%s, error=%v", targetAddr, err)
		return
	}

	// 3. 通过隧道发送 UDP 数据报
	datagram := &UDPDatagram{Data: data}
	if err := datagram.WriteTo(session.Stream); err != nil {
		h.log.Error("发送 UDP 数据报失败: error=%v", err)
		h.closeSession(assoc, targetAddr)
		return
	}

	session.mu.Lock()
	session.LastActive = time.Now()
	session.mu.Unlock()

	h.log.Debug("UDP 数据已通过隧道发送: target=%s, size=%d", targetAddr, len(data))
}

// getOrCreateSession 获取现有会话或创建新会话
func (h *UDPAssociateHandler) getOrCreateSession(
	assoc *UDPAssociation,
	targetAddr, host string,
	port uint16,
	clientUDP *net.UDPAddr,
) (*UDPSession, error) {
	// 检查现有会话
	if v, ok := assoc.sessions.Load(targetAddr); ok {
		return v.(*UDPSession), nil
	}

	// 检查隧道管理器是否可用
	if h.handler.tunnelMgr == nil {
		return nil, fmt.Errorf("隧道管理器未初始化")
	}

	// 通过隧道管理器打开 UDP 流
	stream, err := h.handler.tunnelMgr.OpenStream("udp", host, port)
	if err != nil {
		return nil, fmt.Errorf("打开隧道流失败: %w", err)
	}

	session := &UDPSession{
		TargetAddr: targetAddr,
		Stream:     stream,
		LastActive: time.Now(),
	}

	// 存储会话
	actual, loaded := assoc.sessions.LoadOrStore(targetAddr, session)
	if loaded {
		// 另一个 goroutine 已创建，关闭我们的
		stream.Close()
		return actual.(*UDPSession), nil
	}

	// 启动响应接收器
	go h.handleSessionResponses(assoc, session, clientUDP)

	h.log.Debug("创建新 UDP 会话: target=%s", targetAddr)
	return session, nil
}

// handleSessionResponses 处理从隧道返回的 UDP 响应
func (h *UDPAssociateHandler) handleSessionResponses(
	assoc *UDPAssociation,
	session *UDPSession,
	clientUDP *net.UDPAddr,
) {
	defer h.closeSession(assoc, session.TargetAddr)

	for {
		// 读取响应数据报
		datagram := &UDPDatagram{}
		if err := datagram.ReadFrom(session.Stream); err != nil {
			if err != io.EOF {
				h.log.Debug("读取 UDP 响应失败: error=%v", err)
			}
			return
		}

		session.mu.Lock()
		session.LastActive = time.Now()
		session.mu.Unlock()

		// 构建 SOCKS5 UDP 响应并发送给客户端
		response := h.buildUDPResponse(session.TargetAddr, datagram.Data)

		assoc.mu.Lock()
		if assoc.closed {
			assoc.mu.Unlock()
			return
		}
		_, err := assoc.UDPConn.WriteToUDP(response, clientUDP)
		assoc.mu.Unlock()

		if err != nil {
			h.log.Debug("发送 UDP 响应给客户端失败: error=%v", err)
			return
		}

		h.log.Debug("UDP 响应已转发给客户端: target=%s, size=%d",
			session.TargetAddr, len(datagram.Data))
	}
}

// closeSession 关闭 UDP 会话
func (h *UDPAssociateHandler) closeSession(assoc *UDPAssociation, targetAddr string) {
	if v, ok := assoc.sessions.LoadAndDelete(targetAddr); ok {
		session := v.(*UDPSession)
		if session.Stream != nil {
			session.Stream.Close()
		}
		h.log.Debug("关闭 UDP 会话: target=%s", targetAddr)
	}
}

// closeAllSessions 关闭关联的所有会话
func (h *UDPAssociateHandler) closeAllSessions(assoc *UDPAssociation) {
	assoc.sessions.Range(func(key, value interface{}) bool {
		session := value.(*UDPSession)
		if session.Stream != nil {
			session.Stream.Close()
		}
		assoc.sessions.Delete(key)
		return true
	})
}

func (h *UDPAssociateHandler) buildUDPResponse(targetAddr string, data []byte) []byte {
	host, portStr, _ := net.SplitHostPort(targetAddr)
	port, _ := net.LookupPort("udp", portStr)

	var response []byte
	response = append(response, 0, 0, 0) // RSV + FRAG

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

		// 关闭所有会话
		h.closeAllSessions(assoc)

		assoc.UDPConn.Close()
		h.log.Debug("关闭 UDP 关联: id=%s", id)
	}
}

func (h *UDPAssociateHandler) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		h.associations.Range(func(key, value interface{}) bool {
			assoc := value.(*UDPAssociation)

			// 清理过期的会话
			assoc.sessions.Range(func(skey, svalue interface{}) bool {
				session := svalue.(*UDPSession)
				session.mu.Lock()
				lastActive := session.LastActive
				session.mu.Unlock()

				if now.Sub(lastActive) > h.timeout {
					h.closeSession(assoc, skey.(string))
				}
				return true
			})

			// 检查关联是否过期
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

// GetActiveAssociations 返回活跃关联数量（用于监控）
func (h *UDPAssociateHandler) GetActiveAssociations() int {
	count := 0
	h.associations.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// GetActiveSessions 返回活跃会话数量（用于监控）
func (h *UDPAssociateHandler) GetActiveSessions() int {
	count := 0
	h.associations.Range(func(_, value interface{}) bool {
		assoc := value.(*UDPAssociation)
		assoc.sessions.Range(func(_, _ interface{}) bool {
			count++
			return true
		})
		return true
	})
	return count
}
