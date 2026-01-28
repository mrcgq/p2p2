
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/anthropics/phantom-core/internal/logger"
	"github.com/anthropics/phantom-core/internal/stats"
	"github.com/anthropics/phantom-core/internal/tunnel"
)

// Server HTTP API 服务器
type Server struct {
	addr      string
	tunnelMgr *tunnel.Manager
	stats     *stats.Collector
	log       *logger.Logger

	server *http.Server
	wsMgr  *WSManager
}

// NewServer 创建新的 API 服务器
func NewServer(addr string, tunnelMgr *tunnel.Manager, stats *stats.Collector, log *logger.Logger) *Server {
	return &Server{
		addr:      addr,
		tunnelMgr: tunnelMgr,
		stats:     stats,
		log:       log,
		wsMgr:     NewWSManager(),
	}
}

// Start 启动 API 服务器
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// API 路由
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/config/server", s.handleServerConfig)
	mux.HandleFunc("/api/sysproxy", s.handleSysProxy)
	mux.HandleFunc("/ws", s.handleWebSocket)

	s.server = &http.Server{
		Addr:    s.addr,
		Handler: corsMiddleware(mux),
	}

	go s.broadcastStats(ctx)

	s.log.Info("API 服务器监听于 http://%s", s.addr)

	go func() {
		<-ctx.Done()
		s.server.Close()
	}()

	return s.server.ListenAndServe()
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *Server) broadcastStats(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := s.stats.GetStats()
			stats.Connected = s.tunnelMgr.IsConnected()
			s.wsMgr.Broadcast(map[string]interface{}{
				"type": "stats",
				"data": stats,
			})
		}
	}
}


