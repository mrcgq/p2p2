
package buffer

import (
	"sync"
	"time"
)

// SendBuffer 管理发送数据的缓冲区，带速率限制
type SendBuffer struct {
	buf          []byte
	maxSize      int
	maxBurst     int
	sendInterval time.Duration

	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
}

// NewSendBuffer 创建新的发送缓冲区
func NewSendBuffer(maxSize, maxBurst int, sendInterval time.Duration) *SendBuffer {
	b := &SendBuffer{
		buf:          make([]byte, 0, maxSize),
		maxSize:      maxSize,
		maxBurst:     maxBurst,
		sendInterval: sendInterval,
	}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Write 向缓冲区添加数据，满时阻塞
func (b *SendBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return 0, ErrBufferClosed
	}

	for len(b.buf)+len(data) > b.maxSize && !b.closed {
		b.cond.Wait()
	}

	if b.closed {
		return 0, ErrBufferClosed
	}

	b.buf = append(b.buf, data...)
	b.cond.Broadcast()

	return len(data), nil
}

// Read 从缓冲区读取数据（最多 maxBurst 字节）
func (b *SendBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for len(b.buf) == 0 && !b.closed {
		b.cond.Wait()
	}

	if len(b.buf) == 0 && b.closed {
		return 0, ErrBufferClosed
	}

	n := len(b.buf)
	if n > b.maxBurst {
		n = b.maxBurst
	}
	if n > len(p) {
		n = len(p)
	}

	copy(p, b.buf[:n])
	b.buf = b.buf[n:]

	b.cond.Broadcast()
	return n, nil
}

// Len 返回当前缓冲区长度
func (b *SendBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf)
}

// Close 关闭缓冲区
func (b *SendBuffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true
	b.cond.Broadcast()
	return nil
}

// RecvBuffer 管理接收数据的缓冲区
type RecvBuffer struct {
	buf     []byte
	maxSize int

	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
}

// NewRecvBuffer 创建新的接收缓冲区
func NewRecvBuffer(maxSize int) *RecvBuffer {
	b := &RecvBuffer{
		buf:     make([]byte, 0, maxSize),
		maxSize: maxSize,
	}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Write 添加接收的数据到缓冲区
func (b *RecvBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return 0, ErrBufferClosed
	}

	if len(b.buf)+len(data) > b.maxSize {
		return 0, ErrBufferFull
	}

	b.buf = append(b.buf, data...)
	b.cond.Broadcast()

	return len(data), nil
}

// Read 从缓冲区读取数据
func (b *RecvBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for len(b.buf) == 0 && !b.closed {
		b.cond.Wait()
	}

	if len(b.buf) == 0 && b.closed {
		return 0, ErrBufferClosed
	}

	n := copy(p, b.buf)
	b.buf = b.buf[n:]

	b.cond.Broadcast()
	return n, nil
}

// Len 返回当前缓冲区长度
func (b *RecvBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf)
}

// Close 关闭缓冲区
func (b *RecvBuffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true
	b.cond.Broadcast()
	return nil
}

// 错误
var (
	ErrBufferClosed = &BufferError{"缓冲区已关闭"}
	ErrBufferFull   = &BufferError{"缓冲区已满"}
)

type BufferError struct {
	msg string
}

func (e *BufferError) Error() string {
	return e.msg
}

