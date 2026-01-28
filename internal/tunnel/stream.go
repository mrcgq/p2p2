
package tunnel

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropics/phantom-core/internal/buffer"
)

var (
	ErrStreamClosed = errors.New("流已关闭")
	ErrStreamReset  = errors.New("流已重置")
)

// StreamState 流状态
type StreamState uint32

const (
	StreamStateIdle StreamState = iota
	StreamStateOpen
	StreamStateHalfClosed
	StreamStateClosed
	StreamStateReset
)

// Stream 表示多路复用中的单个流
type Stream struct {
	id        uint32
	sessionID uint32
	state     atomic.Uint32

	recvBuf *buffer.RecvBuffer
	sendBuf *buffer.SendBuffer

	sendSeq atomic.Uint32
	recvSeq atomic.Uint32

	network    string
	targetAddr string

	created    time.Time
	lastActive atomic.Value

	onClose func()
	mu      sync.Mutex
}

// StreamConfig 流配置
type StreamConfig struct {
	BufferSize int
}

// NewStream 创建新流
func NewStream(id, sessionID uint32, config StreamConfig) *Stream {
	s := &Stream{
		id:        id,
		sessionID: sessionID,
		recvBuf:   buffer.NewRecvBuffer(config.BufferSize),
		sendBuf:   buffer.NewSendBuffer(config.BufferSize, 4096, time.Microsecond*100),
		created:   time.Now(),
	}
	s.state.Store(uint32(StreamStateOpen))
	s.lastActive.Store(time.Now())
	return s
}

// ID 返回流 ID
func (s *Stream) ID() uint32 {
	return s.id
}

// SessionID 返回会话 ID
func (s *Stream) SessionID() uint32 {
	return s.sessionID
}

// State 返回流状态
func (s *Stream) State() StreamState {
	return StreamState(s.state.Load())
}

// Touch 更新最后活动时间
func (s *Stream) Touch() {
	s.lastActive.Store(time.Now())
}

// Read 实现 io.Reader
func (s *Stream) Read(p []byte) (n int, err error) {
	if s.State() >= StreamStateClosed {
		return 0, io.EOF
	}
	n, err = s.recvBuf.Read(p)
	if err != nil {
		if s.State() >= StreamStateClosed {
			return 0, io.EOF
		}
	}
	s.Touch()
	return n, err
}

// Write 实现 io.Writer
func (s *Stream) Write(p []byte) (n int, err error) {
	if s.State() >= StreamStateClosed {
		return 0, ErrStreamClosed
	}
	n, err = s.sendBuf.Write(p)
	s.Touch()
	return n, err
}

// Close 关闭流
func (s *Stream) Close() error {
	if !s.state.CompareAndSwap(uint32(StreamStateOpen), uint32(StreamStateClosed)) {
		return nil
	}

	s.recvBuf.Close()
	s.sendBuf.Close()

	if s.onClose != nil {
		go s.onClose()
	}

	return nil
}

// Reset 重置流
func (s *Stream) Reset() {
	s.state.Store(uint32(StreamStateReset))
	s.recvBuf.Close()
	s.sendBuf.Close()

	if s.onClose != nil {
		go s.onClose()
	}
}

// PushData 推送接收到的数据
func (s *Stream) PushData(data []byte) error {
	if s.State() >= StreamStateClosed {
		return ErrStreamClosed
	}
	_, err := s.recvBuf.Write(data)
	s.Touch()
	return err
}

// ReadSend 从发送缓冲区读取
func (s *Stream) ReadSend(p []byte) (int, error) {
	return s.sendBuf.Read(p)
}

// NextSendSeq 返回并递增发送序列号
func (s *Stream) NextSendSeq() uint32 {
	return s.sendSeq.Add(1)
}

// UpdateRecvSeq 更新接收序列号
func (s *Stream) UpdateRecvSeq(seq uint32) {
	for {
		old := s.recvSeq.Load()
		if seq <= old {
			return
		}
		if s.recvSeq.CompareAndSwap(old, seq) {
			return
		}
	}
}

// GetRecvSeq 获取当前接收序列号
func (s *Stream) GetRecvSeq() uint32 {
	return s.recvSeq.Load()
}

// SetTarget 设置目标信息
func (s *Stream) SetTarget(network, addr string) {
	s.network = network
	s.targetAddr = addr
}

// GetTarget 获取目标信息
func (s *Stream) GetTarget() (string, string) {
	return s.network, s.targetAddr
}

// SetOnClose 设置关闭回调
func (s *Stream) SetOnClose(fn func()) {
	s.onClose = fn
}

// IsClosed 检查流是否已关闭
func (s *Stream) IsClosed() bool {
	return s.State() >= StreamStateClosed
}

