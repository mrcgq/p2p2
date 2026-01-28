
package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Level 表示日志级别
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = map[Level]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO ",
	LevelWarn:  "WARN ",
	LevelError: "ERROR",
}

var levelColors = map[Level]string{
	LevelDebug: "\033[36m",
	LevelInfo:  "\033[32m",
	LevelWarn:  "\033[33m",
	LevelError: "\033[31m",
}

const colorReset = "\033[0m"

// Logger 高性能日志器
type Logger struct {
	level    Level
	output   io.Writer
	file     *os.File
	useColor bool
	mu       sync.Mutex

	bufPool sync.Pool
}

// New 创建新的日志器
func New(level string, filePath string) (*Logger, error) {
	l := &Logger{
		level:    parseLevel(level),
		useColor: true,
		bufPool: sync.Pool{
			New: func() interface{} {
				return make([]byte, 0, 256)
			},
		},
	}

	if filePath == "" {
		l.output = os.Stdout
	} else {
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("创建日志目录: %w", err)
		}

		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("打开日志文件: %w", err)
		}
		l.file = f
		l.output = f
		l.useColor = false
	}

	return l, nil
}

func parseLevel(s string) Level {
	switch s {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func (l *Logger) log(level Level, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	buf := l.bufPool.Get().([]byte)
	buf = buf[:0]
	defer l.bufPool.Put(buf)

	now := time.Now()
	buf = now.AppendFormat(buf, "2006-01-02 15:04:05.000")
	buf = append(buf, ' ')

	levelName := levelNames[level]
	if l.useColor {
		buf = append(buf, levelColors[level]...)
		buf = append(buf, '[')
		buf = append(buf, levelName...)
		buf = append(buf, ']')
		buf = append(buf, colorReset...)
	} else {
		buf = append(buf, '[')
		buf = append(buf, levelName...)
		buf = append(buf, ']')
	}
	buf = append(buf, ' ')

	message := fmt.Sprintf(format, args...)
	buf = append(buf, message...)
	buf = append(buf, '\n')

	l.mu.Lock()
	l.output.Write(buf)
	l.mu.Unlock()
}

// Debug 记录调试消息
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LevelDebug, format, args...)
}

// Info 记录信息消息
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LevelInfo, format, args...)
}

// Warn 记录警告消息
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LevelWarn, format, args...)
}

// Error 记录错误消息
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LevelError, format, args...)
}

// Close 关闭日志器
func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// SetLevel 设置日志级别
func (l *Logger) SetLevel(level string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = parseLevel(level)
}

// IsDebug 是否为调试级别
func (l *Logger) IsDebug() bool {
	return l.level == LevelDebug
}
