
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/anthropics/phantom-core/internal/api"
	"github.com/anthropics/phantom-core/internal/config"
	"github.com/anthropics/phantom-core/internal/logger"
	"github.com/anthropics/phantom-core/internal/proxy"
	"github.com/anthropics/phantom-core/internal/stats"
	"github.com/anthropics/phantom-core/internal/tunnel"
)

var (
	Version   = "1.1.0"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Parse flags
	configPath := flag.String("c", "", "配置文件路径")
	apiAddr := flag.String("api", "127.0.0.1:19080", "API 监听地址")
	socksAddr := flag.String("socks", "127.0.0.1:1080", "SOCKS5 监听地址")
	httpAddr := flag.String("http", "127.0.0.1:1081", "HTTP 代理监听地址")
	logLevel := flag.String("log", "info", "日志级别 (debug/info/warn/error)")
	showVersion := flag.Bool("version", false, "显示版本信息")
	flag.Parse()

	if *showVersion {
		fmt.Printf(`Phantom Core v%s
  构建时间: %s
  Git 提交: %s
  Go 版本:  %s
  操作系统: %s/%s
`, Version, BuildTime, GitCommit, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}

	// Initialize logger
	log, err := logger.New(*logLevel, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}

	log.Info("╔═══════════════════════════════════════════════════════════════╗")
	log.Info("║              Phantom Core v%s                              ║", Version)
	log.Info("║          战术级隐匿代理协议 - 客户端内核                      ║")
	log.Info("╚═══════════════════════════════════════════════════════════════╝")

	// Load config if provided
	var cfg *config.Config
	if *configPath != "" {
		cfg, err = config.Load(*configPath)
		if err != nil {
			log.Error("加载配置失败: %v", err)
			os.Exit(1)
		}
	} else {
		cfg = config.Default()
	}

	// Override with flags
	if *socksAddr != "" {
		cfg.Proxy.SocksAddr = *socksAddr
	}
	if *httpAddr != "" {
		cfg.Proxy.HTTPAddr = *httpAddr
	}
	if *apiAddr != "" {
		cfg.API.Addr = *apiAddr
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize components
	statsCollector := stats.New()

	// Create tunnel manager
	tunnelMgr := tunnel.NewManager(cfg, statsCollector, log)

	// Create proxy servers
	proxyHandler := proxy.NewHandler(tunnelMgr, statsCollector, log)
	socksServer := proxy.NewSocks5Server(cfg.Proxy.SocksAddr, cfg.Proxy.SocksUDPEnabled, proxyHandler, log)
	httpServer := proxy.NewHTTPServer(cfg.Proxy.HTTPAddr, proxyHandler, log)

	// Create API server
	apiServer := api.NewServer(cfg.API.Addr, tunnelMgr, statsCollector, log)

	// Start components
	go func() {
		if err := socksServer.Start(ctx); err != nil {
			log.Error("SOCKS5 服务器错误: %v", err)
		}
	}()

	go func() {
		if err := httpServer.Start(ctx); err != nil {
			log.Error("HTTP 代理错误: %v", err)
		}
	}()

	go func() {
		if err := apiServer.Start(ctx); err != nil {
			log.Error("API 服务器错误: %v", err)
		}
	}()

	log.Info("")
	log.Info("┌─ 服务状态 ─────────────────────────────────────────────────────")
	log.Info("│  API:    http://%s", cfg.API.Addr)
	log.Info("│  SOCKS5: %s (UDP: %v)", cfg.Proxy.SocksAddr, cfg.Proxy.SocksUDPEnabled)
	log.Info("│  HTTP:   %s", cfg.Proxy.HTTPAddr)
	log.Info("└─────────────────────────────────────────────────────────────────")
	log.Info("")
	log.Info("等待连接服务器...")

	// Wait for shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info("正在关闭...")
	cancel()

	// Cleanup
	tunnelMgr.Close()
	log.Info("Phantom Core 已停止")
}
