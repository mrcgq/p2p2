
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/anthropics/phantom-core/internal/config"
	"github.com/anthropics/phantom-core/internal/sysproxy"
)

// StatusResponse 状态响应
type StatusResponse struct {
	Connected    bool    `json:"connected"`
	ServerAddr   string  `json:"server_addr"`
	Mode         string  `json:"mode"`
	MuxEnabled   bool    `json:"mux_enabled"`
	FECEnabled   bool    `json:"fec_enabled"`
	FECMode      string  `json:"fec_mode"`
	CurrentParity int32  `json:"current_parity"`
	LossRate     float64 `json:"loss_rate"`
	StreamCount  int     `json:"stream_count"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.tunnelMgr.GetConfig()
	stats := s.stats.GetStats()

	resp := StatusResponse{
		Connected:     s.tunnelMgr.IsConnected(),
		ServerAddr:    cfg.Server.Address,
		Mode:          cfg.Server.Mode,
		MuxEnabled:    cfg.Mux.Enabled,
		FECEnabled:    cfg.FEC.Enabled,
		FECMode:       cfg.FEC.Mode,
		CurrentParity: stats.CurrentParity,
		LossRate:      stats.LossRate,
	}

	if tunnel := s.tunnelMgr.GetTunnel(); tunnel != nil {
		if mux := tunnel.GetMux(); mux != nil {
			resp.StreamCount = mux.StreamCount()
		}
	}

	writeJSON(w, resp)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}

	if err := s.tunnelMgr.Connect(context.Background()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]bool{"success": true})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}

	s.tunnelMgr.Disconnect()
	writeJSON(w, map[string]bool{"success": true})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := s.stats.GetStats()
	stats.Connected = s.tunnelMgr.IsConnected()
	writeJSON(w, stats)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		writeJSON(w, s.tunnelMgr.GetConfig())

	case "PUT":
		var cfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, "无效配置")
			return
		}
		writeJSON(w, map[string]bool{"success": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

func (s *Server) handleServerConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" && r.Method != "POST" {
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}

	var req struct {
		Address    string `json:"address"`
		TCPPort    int    `json:"tcp_port"`
		UDPPort    int    `json:"udp_port"`
		PSK        string `json:"psk"`
		Mode       string `json:"mode"`
		TLSEnabled bool   `json:"tls_enabled"`
		ServerName string `json:"server_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "无效请求")
		return
	}

	s.tunnelMgr.Disconnect()

	serverCfg := config.ServerConfig{
		Address:    req.Address,
		TCPPort:    req.TCPPort,
		UDPPort:    req.UDPPort,
		PSK:        req.PSK,
		Mode:       req.Mode,
		TimeWindow: 30,
		TLS: config.TLSConfig{
			Enabled:    req.TLSEnabled,
			ServerName: req.ServerName,
		},
	}
	s.tunnelMgr.SetServer(serverCfg)

	writeJSON(w, map[string]bool{"success": true})
}

func (s *Server) handleSysProxy(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		enabled, err := sysproxy.IsEnabled()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"enabled": enabled})

	case "POST":
		var req struct {
			Enable bool `json:"enable"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "无效请求")
			return
		}

		cfg := s.tunnelMgr.GetConfig()

		if req.Enable {
			if err := sysproxy.Enable(cfg.Proxy.HTTPAddr); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		} else {
			if err := sysproxy.Disable(); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		writeJSON(w, map[string]bool{"success": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

