// internal/proxy/http.go

package proxy

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/anthropics/phantom-core/internal/logger"
)

// HTTPServer HTTP 代理服务器
type HTTPServer struct {
	addr     string
	handler  *Handler
	log      *logger.Logger
	listener net.Listener
}

// NewHTTPServer 创建新的 HTTP 代理服务器
func NewHTTPServer(addr string, handler *Handler, log *logger.Logger) *HTTPServer {
	return &HTTPServer{
		addr:    addr,
		handler: handler,
		log:     log,
	}
}

// Start 启动 HTTP 代理服务器
func (s *HTTPServer) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("监听: %w", err)
	}
	s.listener = listener

	s.log.Info("HTTP 代理服务器监听于 %s", s.addr)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				s.log.Debug("接受连接错误: %v", err)
				continue
			}
		}

		go s.handleConn(conn)
	}
}

func (s *HTTPServer) handleConn(conn net.Conn) {
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(30 * time.Second))

	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}

	if req.Method == "CONNECT" {
		s.handleConnect(conn, req)
	} else {
		s.handleHTTP(conn, req)
	}
}

func (s *HTTPServer) handleConnect(conn net.Conn, req *http.Request) {
	host, port, err := parseHostPort(req.Host, "443")
	if err != nil {
		conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	s.log.Debug("HTTP CONNECT: %s:%d", host, port)

	conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	conn.SetDeadline(time.Time{})

	s.handler.HandleConn("tcp", host, uint16(port), conn)
}

func (s *HTTPServer) handleHTTP(conn net.Conn, req *http.Request) {
	host, port, err := parseHostPort(req.Host, "80")
	if err != nil {
		conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	s.log.Debug("HTTP 请求: %s %s", req.Method, req.URL)

	conn.SetDeadline(time.Time{})

	s.handler.HandleConn("tcp", host, uint16(port), conn)
}

func parseHostPort(hostPort, defaultPort string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		portStr = defaultPort
	}

	var port int
	fmt.Sscanf(portStr, "%d", &port)
	if port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("无效端口")
	}

	return host, port, nil
}

// Stop 停止服务器
func (s *HTTPServer) Stop() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}
