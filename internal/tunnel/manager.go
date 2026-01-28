
package tunnel

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/anthropics/phantom-core/internal/config"
	"github.com/anthropics/phantom-core/internal/logger"
	"github.com/anthropics/phantom-core/internal/stats"
)

// Manager 管理隧道和服务器连接
type Manager struct {
	config *config.Config
	tunnel *Tunnel
	stats  *stats.Collector
	log    *logger.Logger

	mu sync.Mutex
}

// NewManager 创建新的隧道管理器
func NewManager(cfg *config.Config, statsCollector *stats.Collector, log *logger.Logger) *Manager {
	return &Manager{
		config: cfg,
		stats:  statsCollector,
		log:    log,
	}
}

// SetServer 设置服务器配置
func (m *Manager) SetServer(serverCfg config.ServerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Server = serverCfg
}

// Connect 连接到服务器
func (m *Manager) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tunnel != nil && m.tunnel.IsConnected() {
		return nil
	}

	tunnel, err := New(m.config, m.stats, m.log)
	if err != nil {
		return err
	}
	m.tunnel = tunnel

	return m.tunnel.Connect(ctx)
}

// Disconnect 断开与服务器的连接
func (m *Manager) Disconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tunnel != nil {
		m.tunnel.Disconnect()
	}
}

// IsConnected 返回连接状态
func (m *Manager) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tunnel == nil {
		return false
	}
	return m.tunnel.IsConnected()
}

// OpenStream 打开流（多路复用模式）
func (m *Manager) OpenStream(network, address string, port uint16) (*Stream, error) {
	m.mu.Lock()
	tunnel := m.tunnel
	m.mu.Unlock()

	if tunnel == nil {
		return nil, ErrNotConnected
	}

	return tunnel.OpenStream(network, address, port)
}

// OpenSession 打开会话（非多路复用模式）
func (m *Manager) OpenSession(network, address string, port uint16) (*Session, error) {
	m.mu.Lock()
	tunnel := m.tunnel
	m.mu.Unlock()

	if tunnel == nil {
		return nil, ErrNotConnected
	}

	return tunnel.OpenSession(network, address, port)
}

// SendStreamData 发送流数据
func (m *Manager) SendStreamData(streamID uint32, data []byte) error {
	m.mu.Lock()
	tunnel := m.tunnel
	m.mu.Unlock()

	if tunnel == nil {
		return ErrNotConnected
	}

	return tunnel.SendStreamData(streamID, data)
}

// SendData 发送会话数据
func (m *Manager) SendData(sessionID uint32, data []byte) error {
	m.mu.Lock()
	tunnel := m.tunnel
	m.mu.Unlock()

	if tunnel == nil {
		return ErrNotConnected
	}

	return tunnel.SendData(sessionID, data)
}

// CloseStream 关闭流
func (m *Manager) CloseStream(streamID uint32) error {
	m.mu.Lock()
	tunnel := m.tunnel
	m.mu.Unlock()

	if tunnel == nil {
		return nil
	}

	return tunnel.CloseStream(streamID)
}

// CloseSession 关闭会话
func (m *Manager) CloseSession(sessionID uint32) error {
	m.mu.Lock()
	tunnel := m.tunnel
	m.mu.Unlock()

	if tunnel == nil {
		return nil
	}

	return tunnel.CloseSession(sessionID)
}

// GetTunnel 返回当前隧道
func (m *Manager) GetTunnel() *Tunnel {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tunnel
}

// IsMuxEnabled 返回是否启用多路复用
func (m *Manager) IsMuxEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tunnel != nil {
		return m.tunnel.IsMuxEnabled()
	}
	return m.config.Mux.Enabled
}

// Close 关闭管理器
func (m *Manager) Close() {
	m.Disconnect()
}

// GetConfig 返回当前配置
func (m *Manager) GetConfig() *config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config
}

// 错误类型
var ErrNotConnected = &TunnelError{"未连接"}

type TunnelError struct {
	msg string
}

func (e *TunnelError) Error() string {
	return e.msg
}

// SessionManager 管理会话
type SessionManager struct {
	sessions sync.Map
	nextID   atomic.Uint32
}

// NewSessionManager 创建新的会话管理器
func NewSessionManager() *SessionManager {
	return &SessionManager{}
}

// Create 创建新会话
func (m *SessionManager) Create(sendBufSize, recvBufSize int) *Session {
	id := m.nextID.Add(1)
	session := NewSession(id, sendBufSize, recvBufSize)
	m.sessions.Store(id, session)
	return session
}

// Get 获取会话
func (m *SessionManager) Get(id uint32) *Session {
	if v, ok := m.sessions.Load(id); ok {
		return v.(*Session)
	}
	return nil
}

// Delete 删除会话
func (m *SessionManager) Delete(id uint32) {
	if v, ok := m.sessions.LoadAndDelete(id); ok {
		v.(*Session).Close()
	}
}

// CloseAll 关闭所有会话
func (m *SessionManager) CloseAll() {
	m.sessions.Range(func(key, value interface{}) bool {
		value.(*Session).Close()
		m.sessions.Delete(key)
		return true
	})
}

// Count 返回会话数量
func (m *SessionManager) Count() int {
	count := 0
	m.sessions.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

