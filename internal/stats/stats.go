
package stats

import (
	"sync"
	"sync/atomic"
	"time"
)

// Collector 收集流量统计
type Collector struct {
	upload      atomic.Int64
	download    atomic.Int64
	connections atomic.Int64

	uploadHistory   []int64
	downloadHistory []int64
	historyMu       sync.Mutex

	// FEC 统计
	fecRecovered atomic.Uint64
	fecFailed    atomic.Uint64
	currentParity atomic.Int32
	lossRate      atomic.Uint64 // 存储为 uint64，实际为 float64 * 1e6

	startTime time.Time
}

// New 创建新的统计收集器
func New() *Collector {
	c := &Collector{
		uploadHistory:   make([]int64, 60),
		downloadHistory: make([]int64, 60),
		startTime:       time.Now(),
	}

	go c.recordHistory()

	return c
}

// AddUpload 增加上传字节数
func (c *Collector) AddUpload(bytes int64) {
	c.upload.Add(bytes)
}

// AddDownload 增加下载字节数
func (c *Collector) AddDownload(bytes int64) {
	c.download.Add(bytes)
}

// AddConnection 增加连接数
func (c *Collector) AddConnection() {
	c.connections.Add(1)
}

// RemoveConnection 减少连接数
func (c *Collector) RemoveConnection() {
	c.connections.Add(-1)
}

// GetUpload 返回总上传字节数
func (c *Collector) GetUpload() int64 {
	return c.upload.Load()
}

// GetDownload 返回总下载字节数
func (c *Collector) GetDownload() int64 {
	return c.download.Load()
}

// GetConnections 返回当前连接数
func (c *Collector) GetConnections() int64 {
	return c.connections.Load()
}

// GetUploadSpeed 返回上传速度（字节/秒）
func (c *Collector) GetUploadSpeed() int64 {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()

	var total int64
	for _, v := range c.uploadHistory {
		total += v
	}
	return total / 60
}

// GetDownloadSpeed 返回下载速度（字节/秒）
func (c *Collector) GetDownloadSpeed() int64 {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()

	var total int64
	for _, v := range c.downloadHistory {
		total += v
	}
	return total / 60
}

// RecordFECRecovered 记录 FEC 恢复
func (c *Collector) RecordFECRecovered() {
	c.fecRecovered.Add(1)
}

// RecordFECFailed 记录 FEC 失败
func (c *Collector) RecordFECFailed() {
	c.fecFailed.Add(1)
}

// SetCurrentParity 设置当前冗余分片数
func (c *Collector) SetCurrentParity(parity int32) {
	c.currentParity.Store(parity)
}

// SetLossRate 设置丢包率
func (c *Collector) SetLossRate(rate float64) {
	c.lossRate.Store(uint64(rate * 1e6))
}

// GetStats 返回所有统计
func (c *Collector) GetStats() *Stats {
	return &Stats{
		Upload:        c.GetUpload(),
		Download:      c.GetDownload(),
		UploadSpeed:   c.GetUploadSpeed(),
		DownloadSpeed: c.GetDownloadSpeed(),
		Connections:   c.GetConnections(),
		Uptime:        int64(time.Since(c.startTime).Seconds()),
		FECRecovered:  c.fecRecovered.Load(),
		FECFailed:     c.fecFailed.Load(),
		CurrentParity: c.currentParity.Load(),
		LossRate:      float64(c.lossRate.Load()) / 1e6,
	}
}

// Stats 统计数据
type Stats struct {
	Upload        int64   `json:"upload"`
	Download      int64   `json:"download"`
	UploadSpeed   int64   `json:"upload_speed"`
	DownloadSpeed int64   `json:"download_speed"`
	Connections   int64   `json:"connections"`
	Uptime        int64   `json:"uptime"`
	Connected     bool    `json:"connected"`
	FECRecovered  uint64  `json:"fec_recovered"`
	FECFailed     uint64  `json:"fec_failed"`
	CurrentParity int32   `json:"current_parity"`
	LossRate      float64 `json:"loss_rate"`
}

func (c *Collector) recordHistory() {
	var lastUpload, lastDownload int64
	index := 0
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		currentUpload := c.upload.Load()
		currentDownload := c.download.Load()

		c.historyMu.Lock()
		c.uploadHistory[index] = currentUpload - lastUpload
		c.downloadHistory[index] = currentDownload - lastDownload
		c.historyMu.Unlock()

		lastUpload = currentUpload
		lastDownload = currentDownload
		index = (index + 1) % 60
	}
}

// Reset 重置所有计数器
func (c *Collector) Reset() {
	c.upload.Store(0)
	c.download.Store(0)
	c.fecRecovered.Store(0)
	c.fecFailed.Store(0)

	c.historyMu.Lock()
	for i := range c.uploadHistory {
		c.uploadHistory[i] = 0
		c.downloadHistory[i] = 0
	}
	c.historyMu.Unlock()
}

