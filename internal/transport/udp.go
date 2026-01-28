
package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// UDPTransport 实现 UDP 传输
type UDPTransport struct {
	config *Config
	conn   *net.UDPConn
	addr   *net.UDPAddr

	readTimeout  time.Duration
	writeTimeout time.Duration

	connected atomic.Bool
	mu        sync.Mutex
}

// NewUDPTransport 创建新的 UDP 传输
func NewUDPTransport(config *Config) *UDPTransport {
	return &UDPTransport{
		config:       config,
		readTimeout:  30 * time.Second,
		writeTimeout: 10 * time.Second,
	}
}

// Dial 连接到 UDP 服务器
func (t *UDPTransport) Dial(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", t.config.Address, t.config.UDPPort)

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("解析地址: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("连接 UDP: %w", err)
	}

	// 设置缓冲区大小
	conn.SetReadBuffer(4 * 1024 * 1024)
	conn.SetWriteBuffer(4 * 1024 * 1024)

	t.conn = conn
	t.addr = udpAddr
	t.connected.Store(true)

	return nil
}

// Send 发送数据到服务器
func (t *UDPTransport) Send(data []byte) error {
	t.mu.Lock()
	conn := t.conn
	t.mu.Unlock()

	if conn == nil || !t.connected.Load() {
		return fmt.Errorf("传输已关闭")
	}

	conn.SetWriteDeadline(time.Now().Add(t.writeTimeout))
	_, err := conn.Write(data)
	return err
}

// Receive 从服务器接收数据
func (t *UDPTransport) Receive() ([]byte, error) {
	t.mu.Lock()
	conn := t.conn
	t.mu.Unlock()

	if conn == nil || !t.connected.Load() {
		return nil, fmt.Errorf("传输已关闭")
	}

	buf := make([]byte, 65535)
	conn.SetReadDeadline(time.Now().Add(t.readTimeout))

	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}

	return buf[:n], nil
}

// Close 关闭传输
func (t *UDPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.connected.Store(false)
	if t.conn != nil {
		err := t.conn.Close()
		t.conn = nil
		return err
	}
	return nil
}

// LocalAddr 返回本地地址
func (t *UDPTransport) LocalAddr() net.Addr {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.conn != nil {
		return t.conn.LocalAddr()
	}
	return nil
}

// RemoteAddr 返回远程地址
func (t *UDPTransport) RemoteAddr() net.Addr {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.addr != nil {
		return t.addr
	}
	return nil
}

// IsConnected 返回连接状态
func (t *UDPTransport) IsConnected() bool {
	return t.connected.Load()
}

// SetReadTimeout 设置读取超时
func (t *UDPTransport) SetReadTimeout(d time.Duration) {
	t.readTimeout = d
}

// SetWriteTimeout 设置写入超时
func (t *UDPTransport) SetWriteTimeout(d time.Duration) {
	t.writeTimeout = d
}

