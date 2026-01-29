

package proxy

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/anthropics/phantom-core/internal/logger"
	"github.com/anthropics/phantom-core/internal/stats"
	"github.com/anthropics/phantom-core/internal/tunnel"
)

// Handler 处理代理连接
type Handler struct {
	tunnelMgr    *tunnel.Manager
	stats        *stats.Collector
	log          *logger.Logger
	bypassHosts  map[string]bool
	bypassMu     sync.RWMutex
}

// NewHandler 创建新的代理处理器
func NewHandler(tunnelMgr *tunnel.Manager, stats *stats.Collector, log *logger.Logger) *Handler {
	h := &Handler{
		tunnelMgr:   tunnelMgr,
		stats:       stats,
		log:         log,
		bypassHosts: make(map[string]bool),
	}
	
	// 添加本地地址到 bypass 列表
	h.bypassHosts["127.0.0.1"] = true
	h.bypassHosts["localhost"] = true
	h.bypassHosts["::1"] = true
	
	return h
}

// SetServerBypass 设置服务器地址为 bypass（避免回环）
func (h *Handler) SetServerBypass(host string) {
	h.bypassMu.Lock()
	defer h.bypassMu.Unlock()
	h.bypassHosts[host] = true
	
	// 也解析 IP 地址
	if ips, err := net.LookupIP(host); err == nil {
		for _, ip := range ips {
			h.bypassHosts[ip.String()] = true
		}
	}
	h.log.Debug("添加服务器地址到 bypass: %s", host)
}

// shouldBypass 检查是否应该绕过代理
func (h *Handler) shouldBypass(host string) bool {
	h.bypassMu.RLock()
	defer h.bypassMu.RUnlock()
	
	// 直接匹配
	if h.bypassHosts[host] {
		return true
	}
	
	// 检查是否为 IP 并匹配
	if ip := net.ParseIP(host); ip != nil {
		if h.bypassHosts[ip.String()] {
			return true
		}
	}
	
	return false
}

// HandleConn 处理代理连接
func (h *Handler) HandleConn(network, address string, port uint16, clientConn net.Conn) {
	defer clientConn.Close()

	// 检查是否应该 bypass（避免回环）
	if h.shouldBypass(address) {
		h.log.Debug("Bypass 直连: %s:%d", address, port)
		h.handleDirect(network, address, port, clientConn)
		return
	}

	// 检查隧道是否已连接
	if !h.tunnelMgr.IsConnected() {
		h.log.Debug("隧道未连接，直连: %s:%d", address, port)
		h.handleDirect(network, address, port, clientConn)
		return
	}

	if h.tunnelMgr.IsMuxEnabled() {
		h.handleMuxConn(network, address, port, clientConn)
	} else {
		h.handleSessionConn(network, address, port, clientConn)
	}
}

// handleDirect 直接连接（不走隧道）
func (h *Handler) handleDirect(network, address string, port uint16, clientConn net.Conn) {
	targetAddr := net.JoinHostPort(address, fmt.Sprintf("%d", port))
	
	targetConn, err := net.DialTimeout(network, targetAddr, 10*time.Second)
	if err != nil {
		h.log.Debug("直连失败: %s - %v", targetAddr, err)
		return
	}
	defer targetConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(targetConn, clientConn)
	}()

	go func() {
		defer wg.Done()
		io.Copy(clientConn, targetConn)
	}()

	wg.Wait()
}

// handleMuxConn 处理多路复用连接
func (h *Handler) handleMuxConn(network, address string, port uint16, clientConn net.Conn) {
	stream, err := h.tunnelMgr.OpenStream(network, address, port)
	if err != nil {
		h.log.Debug("打开流失败: %v，尝试直连", err)
		// 失败时尝试直连
		h.handleDirect(network, address, port, clientConn)
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
		h.log.Debug("打开会话失败: %v，尝试直连", err)
		h.handleDirect(network, address, port, clientConn)
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
