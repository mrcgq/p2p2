
package transport

import (
	"context"
	"net"
)

// Transport 定义传输接口
type Transport interface {
	// Dial 连接到服务器
	Dial(ctx context.Context) error
	// Send 发送数据到服务器
	Send(data []byte) error
	// Receive 从服务器接收数据
	Receive() ([]byte, error)
	// Close 关闭传输
	Close() error
	// LocalAddr 返回本地地址
	LocalAddr() net.Addr
	// RemoteAddr 返回远程地址
	RemoteAddr() net.Addr
	// IsConnected 返回连接状态
	IsConnected() bool
}

// Config 传输配置
type Config struct {
	Address       string
	TCPPort       int
	UDPPort       int
	TLSEnabled    bool
	TLSServerName string
	TLSSkipVerify bool
}
