
package config

import "time"

// Config 客户端核心配置
type Config struct {
	Server ServerConfig `yaml:"server" json:"server"`
	Proxy  ProxyConfig  `yaml:"proxy" json:"proxy"`
	FEC    FECConfig    `yaml:"fec" json:"fec"`
	Mux    MuxConfig    `yaml:"mux" json:"mux"`
	Buffer BufferConfig `yaml:"buffer" json:"buffer"`
	API    APIConfig    `yaml:"api" json:"api"`
	Log    LogConfig    `yaml:"log" json:"log"`
}

// ServerConfig 远程服务器配置
type ServerConfig struct {
	Address    string    `yaml:"address" json:"address"`
	TCPPort    int       `yaml:"tcp_port" json:"tcp_port"`
	UDPPort    int       `yaml:"udp_port" json:"udp_port"`
	PSK        string    `yaml:"psk" json:"psk"`
	TimeWindow int       `yaml:"time_window" json:"time_window"`
	Mode       string    `yaml:"mode" json:"mode"` // "udp" 或 "tcp"
	TLS        TLSConfig `yaml:"tls" json:"tls"`
}

// TLSConfig TLS 配置
type TLSConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled"`
	ServerName string `yaml:"server_name" json:"server_name"`
	SkipVerify bool   `yaml:"skip_verify" json:"skip_verify"`
}

// ProxyConfig 本地代理配置
type ProxyConfig struct {
	SocksAddr       string `yaml:"socks_addr" json:"socks_addr"`
	SocksUDPEnabled bool   `yaml:"socks_udp_enabled" json:"socks_udp_enabled"`
	HTTPAddr        string `yaml:"http_addr" json:"http_addr"`
}

// FECConfig 前向纠错配置
type FECConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled"`
	Mode       string `yaml:"mode" json:"mode"` // "static" 或 "adaptive"
	DataShards int    `yaml:"data_shards" json:"data_shards"`
	FECShards  int    `yaml:"fec_shards" json:"fec_shards"`

	// Adaptive 模式参数
	MinParity      int           `yaml:"min_parity" json:"min_parity"`
	MaxParity      int           `yaml:"max_parity" json:"max_parity"`
	TargetLoss     float64       `yaml:"target_loss" json:"target_loss"`
	AdjustInterval time.Duration `yaml:"adjust_interval" json:"adjust_interval"`
}

// MuxConfig 多路复用配置
type MuxConfig struct {
	Enabled           bool          `yaml:"enabled" json:"enabled"`
	MaxStreams        int           `yaml:"max_streams" json:"max_streams"`
	StreamBuffer      int           `yaml:"stream_buffer" json:"stream_buffer"`
	KeepAliveInterval time.Duration `yaml:"keepalive_interval" json:"keepalive_interval"`
	IdleTimeout       time.Duration `yaml:"idle_timeout" json:"idle_timeout"`
}

// BufferConfig 缓冲区配置
type BufferConfig struct {
	SendBuffer   int           `yaml:"send_buffer" json:"send_buffer"`
	RecvBuffer   int           `yaml:"recv_buffer" json:"recv_buffer"`
	MaxBurstSize int           `yaml:"max_burst_size" json:"max_burst_size"`
	SendInterval time.Duration `yaml:"send_interval" json:"send_interval"`
}

// APIConfig HTTP API 配置
type APIConfig struct {
	Addr string `yaml:"addr" json:"addr"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level string `yaml:"level" json:"level"`
	File  string `yaml:"file" json:"file"`
}

// Default 返回默认配置
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			TimeWindow: 30,
			Mode:       "udp",
			TCPPort:    443,
			UDPPort:    54321,
		},
		Proxy: ProxyConfig{
			SocksAddr:       "127.0.0.1:1080",
			SocksUDPEnabled: true,
			HTTPAddr:        "127.0.0.1:1081",
		},
		FEC: FECConfig{
			Enabled:        true,
			Mode:           "adaptive",
			DataShards:     10,
			FECShards:      3,
			MinParity:      1,
			MaxParity:      8,
			TargetLoss:     0.01,
			AdjustInterval: 5 * time.Second,
		},
		Mux: MuxConfig{
			Enabled:           true,
			MaxStreams:        256,
			StreamBuffer:      65536,
			KeepAliveInterval: 30 * time.Second,
			IdleTimeout:       5 * time.Minute,
		},
		Buffer: BufferConfig{
			SendBuffer:   256 * 1024,
			RecvBuffer:   256 * 1024,
			MaxBurstSize: 32 * 1024,
			SendInterval: time.Microsecond * 100,
		},
		API: APIConfig{
			Addr: "127.0.0.1:19080",
		},
		Log: LogConfig{
			Level: "info",
		},
	}
}
