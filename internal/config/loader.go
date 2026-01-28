
package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load 从文件加载配置
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件: %w", err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件: %w", err)
	}

	return cfg, nil
}

// Save 保存配置到文件
func Save(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化配置: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// ParseShareLink 解析 phantom:// 分享链接
func ParseShareLink(link string) (*Config, error) {
	// 格式: phantom://BASE64(JSON)#name
	if !strings.HasPrefix(link, "phantom://") {
		return nil, fmt.Errorf("无效的分享链接格式")
	}

	link = strings.TrimPrefix(link, "phantom://")

	// 移除 fragment (名称)
	if idx := strings.Index(link, "#"); idx != -1 {
		link = link[:idx]
	}

	// 解码 base64
	data, err := base64.StdEncoding.DecodeString(link)
	if err != nil {
		return nil, fmt.Errorf("解码分享链接: %w", err)
	}

	// 解析 JSON
	var serverInfo struct {
		Address string `json:"address"`
		TCPPort int    `json:"tcp_port"`
		UDPPort int    `json:"udp_port"`
		PSK     string `json:"psk"`
		TLS     struct {
			Enabled    bool   `json:"enabled"`
			ServerName string `json:"server_name"`
		} `json:"tls"`
		FEC struct {
			Enabled    bool   `json:"enabled"`
			Mode       string `json:"mode"`
			DataShards int    `json:"data_shards"`
			FECShards  int    `json:"fec_shards"`
			MinParity  int    `json:"min_parity"`
			MaxParity  int    `json:"max_parity"`
		} `json:"fec"`
		Mux struct {
			Enabled    bool `json:"enabled"`
			MaxStreams int  `json:"max_streams"`
		} `json:"mux"`
		Mode string `json:"mode"`
	}

	if err := json.Unmarshal(data, &serverInfo); err != nil {
		return nil, fmt.Errorf("解析分享链接: %w", err)
	}

	cfg := Default()
	cfg.Server.Address = serverInfo.Address
	cfg.Server.TCPPort = serverInfo.TCPPort
	cfg.Server.UDPPort = serverInfo.UDPPort
	cfg.Server.PSK = serverInfo.PSK
	cfg.Server.Mode = serverInfo.Mode
	cfg.Server.TLS.Enabled = serverInfo.TLS.Enabled
	cfg.Server.TLS.ServerName = serverInfo.TLS.ServerName
	cfg.FEC.Enabled = serverInfo.FEC.Enabled
	cfg.FEC.Mode = serverInfo.FEC.Mode
	if serverInfo.FEC.DataShards > 0 {
		cfg.FEC.DataShards = serverInfo.FEC.DataShards
	}
	if serverInfo.FEC.FECShards > 0 {
		cfg.FEC.FECShards = serverInfo.FEC.FECShards
	}
	if serverInfo.FEC.MinParity > 0 {
		cfg.FEC.MinParity = serverInfo.FEC.MinParity
	}
	if serverInfo.FEC.MaxParity > 0 {
		cfg.FEC.MaxParity = serverInfo.FEC.MaxParity
	}
	cfg.Mux.Enabled = serverInfo.Mux.Enabled
	if serverInfo.Mux.MaxStreams > 0 {
		cfg.Mux.MaxStreams = serverInfo.Mux.MaxStreams
	}

	return cfg, nil
}

// GenerateShareLink 从配置生成分享链接
func GenerateShareLink(cfg *Config, name string) string {
	serverInfo := map[string]interface{}{
		"address":  cfg.Server.Address,
		"tcp_port": cfg.Server.TCPPort,
		"udp_port": cfg.Server.UDPPort,
		"psk":      cfg.Server.PSK,
		"tls": map[string]interface{}{
			"enabled":     cfg.Server.TLS.Enabled,
			"server_name": cfg.Server.TLS.ServerName,
		},
		"fec": map[string]interface{}{
			"enabled":     cfg.FEC.Enabled,
			"mode":        cfg.FEC.Mode,
			"data_shards": cfg.FEC.DataShards,
			"fec_shards":  cfg.FEC.FECShards,
			"min_parity":  cfg.FEC.MinParity,
			"max_parity":  cfg.FEC.MaxParity,
		},
		"mux": map[string]interface{}{
			"enabled":     cfg.Mux.Enabled,
			"max_streams": cfg.Mux.MaxStreams,
		},
		"mode": cfg.Server.Mode,
	}

	data, _ := json.Marshal(serverInfo)
	encoded := base64.StdEncoding.EncodeToString(data)

	return fmt.Sprintf("phantom://%s#%s", encoded, name)
}
