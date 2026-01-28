
package proxy

import (
	"io"
	"net"
	"sync"

	"github.com/anthropics/phantom-core/internal/logger"
	"github.com/anthropics/phantom-core/internal/stats"
	"github.com/anthropics/phantom-core/internal/tunnel"
)

// Handler 处理代理连接
type Handler struct {
	tunnelMgr *tunnel.Manager
	stats     *stats.Collector
	log       *logger.Logger
}

// NewHandler 创建新的代理处理器
func NewHandler(tunnelMgr *tunnel.Manager, stats *stats.Collector, log *logger.Logger) *Handler {
	return &Handler{
		tunnelMgr: tunnelMgr,
		stats:     stats,
		log:       log,
	}
}

// HandleConn 处理代理连接
func (h *Handler) HandleConn(network, address string, port uint16, clientConn net.Conn) {
	defer clientConn.Close()

	if h.tunnelMgr.IsMuxEnabled() {
		h.handleMuxConn(network, address, port, clientConn)
	} else {
		h.handleSessionConn(network, address, port, clientConn)
	}
}

// handleMuxConn 处理多路复用连接
func (h *Handler) handleMuxConn(network, address string, port uint16, clientConn net.Conn) {
	stream, err := h.tunnelMgr.OpenStream(network, address, port)
	if err != nil {
		h.log.Debug("打开流失败: %v", err)
		return
	}
	defer h.tunnelMgr.CloseStream(stream.ID())

	var wg sync.WaitGroup
	wg.Add(2)

	// 客户端 -> 隧道
	go func() {
		defer wg.Done()
		h.relayClientToStream(stream, clientConn)
	}()

	// 隧道 -> 客户端
	go func() {
		defer wg.Done()
		h.relayStreamToClient(stream, clientConn)
	}()

	wg.Wait()
}

// handleSessionConn 处理会话连接（非多路复用）
func (h *Handler) handleSessionConn(network, address string, port uint16, clientConn net.Conn) {
	session, err := h.tunnelMgr.OpenSession(network, address, port)
	if err != nil {
		h.log.Debug("打开会话失败: %v", err)
		return
	}
	defer h.tunnelMgr.CloseSession(session.ID)

	var wg sync.WaitGroup
	wg.Add(2)

	// 客户端 -> 隧道
	go func() {
		defer wg.Done()
		h.relayClientToSession(session, clientConn)
	}()

	// 隧道 -> 客户端
	go func() {
		defer wg.Done()
		h.relaySessionToClient(session, clientConn)
	}()

	wg.Wait()
}

func (h *Handler) relayClientToStream(stream *tunnel.Stream, clientConn net.Conn) {
	buf := make([]byte, 4096)

	for {
		n, err := clientConn.Read(buf)
		if err != nil {
			if err != io.EOF {
				h.log.Debug("客户端读取错误: %v", err)
			}
			return
		}

		if err := h.tunnelMgr.SendStreamData(stream.ID(), buf[:n]); err != nil {
			h.log.Debug("发送流数据错误: %v", err)
			return
		}
	}
}

func (h *Handler) relayStreamToClient(stream *tunnel.Stream, clientConn net.Conn) {
	buf := make([]byte, 4096)

	for {
		n, err := stream.Read(buf)
		if err != nil {
			return
		}

		if _, err := clientConn.Write(buf[:n]); err != nil {
			h.log.Debug("客户端写入错误: %v", err)
			return
		}
	}
}

func (h *Handler) relayClientToSession(session *tunnel.Session, clientConn net.Conn) {
	buf := make([]byte, 4096)

	for {
		n, err := clientConn.Read(buf)
		if err != nil {
			if err != io.EOF {
				h.log.Debug("客户端读取错误: %v", err)
			}
			return
		}

		if err := h.tunnelMgr.SendData(session.ID, buf[:n]); err != nil {
			h.log.Debug("发送数据错误: %v", err)
			return
		}
	}
}

func (h *Handler) relaySessionToClient(session *tunnel.Session, clientConn net.Conn) {
	buf := make([]byte, 4096)

	for {
		n, err := session.ReadRecv(buf)
		if err != nil {
			return
		}

		if _, err := clientConn.Write(buf[:n]); err != nil {
			h.log.Debug("客户端写入错误: %v", err)
			return
		}
	}
}

