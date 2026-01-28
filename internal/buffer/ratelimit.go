
package buffer

import (
	"sync"
	"time"
)

// RateLimiter 限制数据包发送速率
type RateLimiter struct {
	interval time.Duration
	lastSend time.Time
	mu       sync.Mutex
}

// NewRateLimiter 创建新的速率限制器
func NewRateLimiter(interval time.Duration) *RateLimiter {
	return &RateLimiter{
		interval: interval,
	}
}

// Wait 等待直到可以发送
func (r *RateLimiter) Wait() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.interval <= 0 {
		return
	}

	now := time.Now()
	elapsed := now.Sub(r.lastSend)

	if elapsed < r.interval {
		time.Sleep(r.interval - elapsed)
	}

	r.lastSend = time.Now()
}

// TokenBucket 实现令牌桶速率限制
type TokenBucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64
	lastRefill time.Time
	mu         sync.Mutex
}

// NewTokenBucket 创建新的令牌桶
func NewTokenBucket(maxTokens, refillRate float64) *TokenBucket {
	return &TokenBucket{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

// Take 尝试获取 n 个令牌，成功返回 true
func (b *TokenBucket) Take(n float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.refill()

	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}

// TakeWait 获取 n 个令牌，必要时等待
func (b *TokenBucket) TakeWait(n float64) {
	for !b.Take(n) {
		time.Sleep(time.Millisecond * 10)
	}
}

func (b *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.lastRefill = now

	b.tokens += elapsed * b.refillRate
	if b.tokens > b.maxTokens {
		b.tokens = b.maxTokens
	}
}

// Available 返回可用令牌数
func (b *TokenBucket) Available() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	return b.tokens
}

