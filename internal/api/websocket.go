
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// WSManager 管理 WebSocket 连接
type WSManager struct {
	clients map[*websocket.Conn]bool
	mu      sync.RWMutex
}

// NewWSManager 创建新的 WebSocket 管理器
func NewWSManager() *WSManager {
	return &WSManager{
		clients: make(map[*websocket.Conn]bool),
	}
}

// Add 添加客户端
func (m *WSManager) Add(conn *websocket.Conn) {
	m.mu.Lock()
	m.clients[conn] = true
	m.mu.Unlock()
}

// Remove 移除客户端
func (m *WSManager) Remove(conn *websocket.Conn) {
	m.mu.Lock()
	delete(m.clients, conn)
	m.mu.Unlock()
}

// Broadcast 广播消息给所有客户端
func (m *WSManager) Broadcast(data interface{}) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	message, err := json.Marshal(data)
	if err != nil {
		return
	}

	for conn := range m.clients {
		conn.WriteMessage(websocket.TextMessage, message)
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Debug("WebSocket 升级错误: %v", err)
		return
	}
	defer conn.Close()

	s.wsMgr.Add(conn)
	defer s.wsMgr.Remove(conn)

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		s.handleWSMessage(conn, message)
	}
}

func (s *Server) handleWSMessage(conn *websocket.Conn, message []byte) {
	var msg struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}

	if err := json.Unmarshal(message, &msg); err != nil {
		return
	}

	switch msg.Type {
	case "connect":
		go func() {
			err := s.tunnelMgr.Connect(context.Background())
			conn.WriteJSON(map[string]interface{}{
				"type":    "connect_result",
				"success": err == nil,
				"error":   errorString(err),
			})
		}()

	case "disconnect":
		s.tunnelMgr.Disconnect()
		conn.WriteJSON(map[string]interface{}{
			"type":    "disconnect_result",
			"success": true,
		})

	case "get_stats":
		stats := s.stats.GetStats()
		stats.Connected = s.tunnelMgr.IsConnected()
		conn.WriteJSON(map[string]interface{}{
			"type": "stats",
			"data": stats,
		})
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}


