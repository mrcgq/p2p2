
package tunnel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrMuxClosed      = errors.New("多路复用器已关闭")
	ErrMaxStreams     = errors.New("已达到最大流数")
	ErrStreamNotFound = errors.New("流未找到")
)

// MuxConfig 多路复用配置
type MuxConfig struct {
	MaxStreams        int
	StreamBufferSize  int
	KeepAliveInterval time.Duration
	IdleTimeout       time.Duration
}

// Mux 多路复用器
type Mux struct {
	sessionID uint32
	config    MuxConfig

	streams     sync.Map
	streamCount atomic.Int32
	nextID      atomic.Uint32

	closed atomic.Bool

	onNewStream func(*Stream)

	lastPing time.Time
	lastPong time.Time

	mu sync.RWMutex
}

// NewMux 创建新的多路复用器
func NewMux(sessionID uint32, config MuxConfig) *Mux {
	return &Mux{
		sessionID: sessionID,
		config:    config,
		lastPing:  time.Now(),
		lastPong:  time.Now(),
	}
}

// SessionID 返回会话 ID
func (m *Mux) SessionID() uint32 {
	return m.sessionID
}

// OpenStream 打开新流
func (m *Mux) OpenStream(network, targetAddr string) (*Stream, error) {
	if m.closed.Load() {
		return nil, ErrMuxClosed
	}

	if m.streamCount.Load() >= int32(m.config.MaxStreams) {
		return nil, ErrMaxStreams
	}

	id := m.nextID.Add(1)
	stream := NewStream(id, m.sessionID, StreamConfig{
		BufferSize: m.config.StreamBufferSize,
	})
	stream.SetTarget(network, targetAddr)

	m.streams.Store(id, stream)
	m.streamCount.Add(1)

	return stream, nil
}

// AcceptStream 接受新流（由远端发起）
func (m *Mux) AcceptStream(id uint32) (*Stream, error) {
	if m.closed.Load() {
		return nil, ErrMuxClosed
	}

	if m.streamCount.Load() >= int32(m.config.MaxStreams) {
		return nil, ErrMaxStreams
	}

	if _, exists := m.streams.Load(id); exists {
		return nil, errors.New("流已存在")
	}

	stream := NewStream(id, m.sessionID, StreamConfig{
		BufferSize: m.config.StreamBufferSize,
	})

	m.streams.Store(id, stream)
	m.streamCount.Add(1)

	if m.onNewStream != nil {
		go m.onNewStream(stream)
	}

	return stream, nil
}

// GetStream 获取流
func (m *Mux) GetStream(id uint32) (*Stream, bool) {
	if v, ok := m.streams.Load(id); ok {
		return v.(*Stream), true
	}
	return nil, false
}

// CloseStream 关闭流
func (m *Mux) CloseStream(id uint32) error {
	if v, ok := m.streams.LoadAndDelete(id); ok {
		stream := v.(*Stream)
		stream.Close()
		m.streamCount.Add(-1)
		return nil
	}
	return ErrStreamNotFound
}

// Close 关闭多路复用器
func (m *Mux) Close() error {
	if m.closed.Swap(true) {
		return nil
	}

	m.streams.Range(func(key, value interface{}) bool {
		stream := value.(*Stream)
		stream.Close()
		m.streams.Delete(key)
		return true
	})

	m.streamCount.Store(0)
	return nil
}

// IsClosed 检查是否已关闭
func (m *Mux) IsClosed() bool {
	return m.closed.Load()
}

// StreamCount 返回当前流数
func (m *Mux) StreamCount() int {
	return int(m.streamCount.Load())
}

// SetOnNewStream 设置新流回调
func (m *Mux) SetOnNewStream(fn func(*Stream)) {
	m.onNewStream = fn
}

// StartKeepalive 启动心跳
func (m *Mux) StartKeepalive(ctx context.Context, sendPing func() error) {
	if m.config.KeepAliveInterval <= 0 {
		return
	}

	ticker := time.NewTicker(m.config.KeepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.closed.Load() {
				return
			}

			if time.Since(m.lastPong) > m.config.IdleTimeout {
				m.Close()
				return
			}

			m.lastPing = time.Now()
			if err := sendPing(); err != nil {
				continue
			}
		}
	}
}

// OnPong 收到心跳响应
func (m *Mux) OnPong() {
	m.lastPong = time.Now()
}

// RTT 获取 RTT
func (m *Mux) RTT() time.Duration {
	if m.lastPong.After(m.lastPing) {
		return 0
	}
	return m.lastPong.Sub(m.lastPing)
}

// ForEachStream 遍历所有流
func (m *Mux) ForEachStream(fn func(*Stream) bool) {
	m.streams.Range(func(key, value interface{}) bool {
		stream := value.(*Stream)
		return fn(stream)
	})
}

// CleanExpiredStreams 清理过期流
func (m *Mux) CleanExpiredStreams(timeout time.Duration) int {
	now := time.Now()
	count := 0

	m.streams.Range(func(key, value interface{}) bool {
		stream := value.(*Stream)
		lastActive := stream.lastActive.Load().(time.Time)
		if now.Sub(lastActive) > timeout {
			m.CloseStream(stream.ID())
			count++
		}
		return true
	})

	return count
}

