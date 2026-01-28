
package transport

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// TCPTransport 实现 TCP/TLS 传输
type TCPTransport struct {
	config *Config
	conn   net.Conn

	readTimeout  time.Duration
	writeTimeout time.Duration

	connected atomic.Bool
	mu        sync.Mutex
}

// NewTCPTransport 创建新的 TCP 传输
func NewTCPTransport(config *Config) *TCPTransport {
	return &TCPTransport{
		config:       config,
		readTimeout:  30 * time.Second,
		writeTimeout: 10 * time.Second,
	}
}

// Dial 连接到 TCP 服务器
func (t *TCPTransport) Dial(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", t.config.Address, t.config.TCPPort)

	var conn net.Conn
	var err error

	dialer := &net.Dialer{Timeout: 10 * time.Second}

	if t.config.TLSEnabled {
		tlsConfig := &tls.Config{
			ServerName:         t.config.TLSServerName,
			InsecureSkipVerify: t.config.TLSSkipVerify,
			MinVersion:         tls.VersionTLS12,
			NextProtos:         []string{"h2", "http/1.1"},
		}
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}

	if err != nil {
		return fmt.Errorf("连接 TCP: %w", err)
	}

	t.conn = conn
	t.connected.Store(true)

	return nil
}

// Send 发送数据（带长度前缀）
func (t *TCPTransport) Send(data []byte) error {
	t.mu.Lock()
	conn := t.conn
	t.mu.Unlock()

	if conn == nil || !t.connected.Load() {
		return fmt.Errorf("传输已关闭")
	}

	// 长度前缀 (2 字节)
	buf := make([]byte, 2+len(data))
	binary.BigEndian.PutUint16(buf[:2], uint16(len(data)))
	copy(buf[2:], data)

	conn.SetWriteDeadline(time.Now().Add(t.writeTimeout))
	_, err := conn.Write(buf)
	return err
}

// Receive 接收数据（带长度前缀）
func (t *TCPTransport) Receive() ([]byte, error) {
	t.mu.Lock()
	conn := t.conn
	t.mu.Unlock()

	if conn == nil || !t.connected.Load() {
		return nil, fmt.Errorf("传输已关闭")
	}

	conn.SetReadDeadline(time.Now().Add(t.readTimeout))

	// 读取长度前缀
	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint16(lenBuf)
	if length > 65535 {
		return nil, fmt.Errorf("数据包过大")
	}

	// 读取数据
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}

	return data, nil
}

// Close 关闭传输
func (t *TCPTransport) Close() error {
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
func (t *TCPTransport) LocalAddr() net.Addr {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.conn != nil {
		return t.conn.LocalAddr()
	}
	return nil
}

// RemoteAddr 返回远程地址
func (t *TCPTransport) RemoteAddr() net.Addr {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.conn != nil {
		return t.conn.RemoteAddr()
	}
	return nil
}

// IsConnected 返回连接状态
func (t *TCPTransport) IsConnected() bool {
	return t.connected.Load()
}

// SetReadTimeout 设置读取超时
func (t *TCPTransport) SetReadTimeout(d time.Duration) {
	t.readTimeout = d
}

// SetWriteTimeout 设置写入超时
func (t *TCPTransport) SetWriteTimeout(d time.Duration) {
	t.writeTimeout = d
}


