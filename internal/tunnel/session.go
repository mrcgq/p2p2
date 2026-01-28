
package tunnel

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropics/phantom-core/internal/buffer"
)

// Session 表示一个代理会话
type Session struct {
	ID        uint32
	SendBuf   *buffer.SendBuffer
	RecvBuf   *buffer.RecvBuffer

	connected  atomic.Bool
	closed     atomic.Bool
	ackSeq     atomic.Uint32
	sendSeq    atomic.Uint32

	// 目标信息
	network    string
	targetAddr string

	created    time.Time
	lastActive atomic.Value
}

// NewSession 创建新会话
func NewSession(id uint32, sendBufSize, recvBufSize int) *Session {
	s := &Session{
		ID:      id,
		SendBuf: buffer.NewSendBuffer(sendBufSize, 4096, time.Microsecond*100),
		RecvBuf: buffer.NewRecvBuffer(recvBufSize),
		created: time.Now(),
	}
	s.lastActive.Store(time.Now())
	return s
}

// SetConnected 标记会话已连接
func (s *Session) SetConnected() {
	s.connected.Store(true)
}

// IsConnected 返回连接状态
func (s *Session) IsConnected() bool {
	return s.connected.Load()
}

// IsClosed 返回关闭状态
func (s *Session) IsClosed() bool {
	return s.closed.Load()
}

// UpdateAckSeq 更新确认序列号
func (s *Session) UpdateAckSeq(seq uint32) {
	for {
		old := s.ackSeq.Load()
		if seq <= old {
			return
		}
		if s.ackSeq.CompareAndSwap(old, seq) {
			return
		}
	}
}

// GetAckSeq 返回当前确认序列号
func (s *Session) GetAckSeq() uint32 {
	return s.ackSeq.Load()
}

// NextSendSeq 返回并递增发送序列号
func (s *Session) NextSendSeq() uint32 {
	return s.sendSeq.Add(1)
}

// WriteRecv 写入接收缓冲区
func (s *Session) WriteRecv(data []byte) error {
	s.touch()
	_, err := s.RecvBuf.Write(data)
	return err
}

// ReadRecv 从接收缓冲区读取
func (s *Session) ReadRecv(buf []byte) (int, error) {
	s.touch()
	return s.RecvBuf.Read(buf)
}

// WriteSend 写入发送缓冲区
func (s *Session) WriteSend(data []byte) error {
	s.touch()
	_, err := s.SendBuf.Write(data)
	return err
}

// ReadSend 从发送缓冲区读取
func (s *Session) ReadSend(buf []byte) (int, error) {
	s.touch()
	return s.SendBuf.Read(buf)
}

// SetTarget 设置目标信息
func (s *Session) SetTarget(network, addr string) {
	s.network = network
	s.targetAddr = addr
}

// GetTarget 获取目标信息
func (s *Session) GetTarget() (string, string) {
	return s.network, s.targetAddr
}

// Close 关闭会话
func (s *Session) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.SendBuf.Close()
	s.RecvBuf.Close()
}

func (s *Session) touch() {
	s.lastActive.Store(time.Now())
}

// GetLastActive 返回最后活动时间
func (s *Session) GetLastActive() time.Time {
	return s.lastActive.Load().(time.Time)
}


