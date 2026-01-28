
package fec

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/reedsolomon"
)

// AdaptiveConfig 自适应 FEC 配置
type AdaptiveConfig struct {
	DataShards        int
	MinParity         int
	MaxParity         int
	InitialParity     int
	TargetLossRate    float64
	AdjustInterval    time.Duration
	IncreaseThreshold float64
	DecreaseThreshold float64
	StepUp            int
	StepDown          int
}

// DefaultAdaptiveConfig 默认自适应配置
func DefaultAdaptiveConfig() AdaptiveConfig {
	return AdaptiveConfig{
		DataShards:        10,
		MinParity:         1,
		MaxParity:         8,
		InitialParity:     3,
		TargetLossRate:    0.01,
		AdjustInterval:    5 * time.Second,
		IncreaseThreshold: 0.05,
		DecreaseThreshold: 0.01,
		StepUp:            1,
		StepDown:          1,
	}
}

// LossEstimator 丢包率估算器
type LossEstimator struct {
	alpha       float64
	lossRate    float64
	rtt         time.Duration
	sentPackets uint64
	lostPackets uint64
	lastUpdate  time.Time

	mu sync.RWMutex
}

// NewLossEstimator 创建新的丢包估算器
func NewLossEstimator() *LossEstimator {
	return &LossEstimator{
		alpha:      0.125,
		lastUpdate: time.Now(),
	}
}

// RecordSent 记录发送的数据包
func (e *LossEstimator) RecordSent(count uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sentPackets += count
}

// RecordLost 记录丢失的数据包
func (e *LossEstimator) RecordLost(count uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lostPackets += count
	e.updateEstimate()
}

// RecordRTT 记录 RTT 样本
func (e *LossEstimator) RecordRTT(rtt time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.rtt == 0 {
		e.rtt = rtt
	} else {
		e.rtt = time.Duration(float64(e.rtt)*(1-e.alpha) + float64(rtt)*e.alpha)
	}
}

func (e *LossEstimator) updateEstimate() {
	if e.sentPackets == 0 {
		return
	}
	currentLoss := float64(e.lostPackets) / float64(e.sentPackets)
	if e.lossRate == 0 {
		e.lossRate = currentLoss
	} else {
		e.lossRate = e.lossRate*(1-e.alpha) + currentLoss*e.alpha
	}
	e.lastUpdate = time.Now()
}

// GetLossRate 获取当前估计的丢包率
func (e *LossEstimator) GetLossRate() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lossRate
}

// GetRTT 获取估计的 RTT
func (e *LossEstimator) GetRTT() time.Duration {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rtt
}

// Reset 重置估算器
func (e *LossEstimator) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lossRate = 0
	e.rtt = 0
	e.sentPackets = 0
	e.lostPackets = 0
	e.lastUpdate = time.Now()
}

// AdaptiveFEC 自适应前向纠错
type AdaptiveFEC struct {
	config    AdaptiveConfig
	estimator *LossEstimator

	currentParity int
	encoder       reedsolomon.Encoder
	encoderCache  map[int]reedsolomon.Encoder

	adjustCount   int
	lastAdjust    time.Time
	parityHistory []parityRecord

	mu sync.RWMutex
}

type parityRecord struct {
	Timestamp time.Time
	OldParity int
	NewParity int
	LossRate  float64
	Reason    string
}

// NewAdaptiveFEC 创建自适应 FEC
func NewAdaptiveFEC(config AdaptiveConfig) (*AdaptiveFEC, error) {
	if config.MinParity < 1 {
		config.MinParity = 1
	}
	if config.MaxParity < config.MinParity {
		config.MaxParity = config.MinParity
	}
	if config.InitialParity < config.MinParity {
		config.InitialParity = config.MinParity
	}
	if config.InitialParity > config.MaxParity {
		config.InitialParity = config.MaxParity
	}

	encoder, err := reedsolomon.New(config.DataShards, config.InitialParity)
	if err != nil {
		return nil, fmt.Errorf("创建初始编码器: %w", err)
	}

	af := &AdaptiveFEC{
		config:        config,
		estimator:     NewLossEstimator(),
		currentParity: config.InitialParity,
		encoder:       encoder,
		encoderCache:  make(map[int]reedsolomon.Encoder),
		lastAdjust:    time.Now(),
		parityHistory: make([]parityRecord, 0, 100),
	}

	for p := config.MinParity; p <= config.MaxParity; p++ {
		enc, err := reedsolomon.New(config.DataShards, p)
		if err != nil {
			return nil, fmt.Errorf("创建编码器 (parity=%d): %w", p, err)
		}
		af.encoderCache[p] = enc
	}

	return af, nil
}

// Start 启动自适应调整
func (af *AdaptiveFEC) Start(ctx context.Context) {
	go af.adjustLoop(ctx)
}

func (af *AdaptiveFEC) adjustLoop(ctx context.Context) {
	ticker := time.NewTicker(af.config.AdjustInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			af.adjust()
		}
	}
}

func (af *AdaptiveFEC) adjust() {
	af.mu.Lock()
	defer af.mu.Unlock()

	lossRate := af.estimator.GetLossRate()
	oldParity := af.currentParity
	newParity := oldParity
	reason := ""

	if lossRate > af.config.IncreaseThreshold {
		newParity = min(oldParity+af.config.StepUp, af.config.MaxParity)
		reason = fmt.Sprintf("丢包率 %.2f%% > %.2f%%", lossRate*100, af.config.IncreaseThreshold*100)
	} else if lossRate < af.config.DecreaseThreshold && oldParity > af.config.MinParity {
		newParity = max(oldParity-af.config.StepDown, af.config.MinParity)
		reason = fmt.Sprintf("丢包率 %.2f%% < %.2f%%", lossRate*100, af.config.DecreaseThreshold*100)
	}

	if newParity != oldParity {
		if enc, ok := af.encoderCache[newParity]; ok {
			af.encoder = enc
			af.currentParity = newParity
			af.adjustCount++
			af.lastAdjust = time.Now()

			record := parityRecord{
				Timestamp: time.Now(),
				OldParity: oldParity,
				NewParity: newParity,
				LossRate:  lossRate,
				Reason:    reason,
			}
			af.parityHistory = append(af.parityHistory, record)

			if len(af.parityHistory) > 100 {
				af.parityHistory = af.parityHistory[1:]
			}
		}
	}
}

// Encode 编码数据
func (af *AdaptiveFEC) Encode(data []byte) ([][]byte, int, error) {
	if len(data) == 0 {
		return nil, 0, fmt.Errorf("数据不能为空")
	}

	af.mu.RLock()
	encoder := af.encoder
	currentParity := af.currentParity
	dataShards := af.config.DataShards
	af.mu.RUnlock()

	totalShards := dataShards + currentParity
	shardSize := (len(data) + dataShards - 1) / dataShards

	shards := make([][]byte, totalShards)
	for i := 0; i < totalShards; i++ {
		shards[i] = make([]byte, shardSize)
	}

	offset := 0
	for i := 0; i < dataShards; i++ {
		if offset >= len(data) {
			break
		}
		n := copy(shards[i], data[offset:])
		offset += n
	}

	if err := encoder.Encode(shards); err != nil {
		return nil, 0, fmt.Errorf("编码: %w", err)
	}

	af.estimator.RecordSent(uint64(totalShards))

	return shards, currentParity, nil
}

// Decode 解码数据
func (af *AdaptiveFEC) Decode(shards [][]byte, dataLen int, parityUsed int) ([]byte, error) {
	af.mu.RLock()
	dataShards := af.config.DataShards
	af.mu.RUnlock()

	totalShards := dataShards + parityUsed

	if len(shards) != totalShards {
		return nil, fmt.Errorf("分片数量错误: 期望 %d, 实际 %d", totalShards, len(shards))
	}

	encoder, ok := af.encoderCache[parityUsed]
	if !ok {
		var err error
		encoder, err = reedsolomon.New(dataShards, parityUsed)
		if err != nil {
			return nil, fmt.Errorf("创建解码器: %w", err)
		}
	}

	received := 0
	for _, shard := range shards {
		if shard != nil && len(shard) > 0 {
			received++
		}
	}
	lost := totalShards - received

	if lost > 0 {
		af.estimator.RecordLost(uint64(lost))
	}

	if received < dataShards {
		return nil, fmt.Errorf("分片不足: 需要 %d, 可用 %d", dataShards, received)
	}

	if err := encoder.Reconstruct(shards); err != nil {
		return nil, fmt.Errorf("重建: %w", err)
	}

	ok, err := encoder.Verify(shards)
	if err != nil {
		return nil, fmt.Errorf("验证: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("验证失败")
	}

	data := make([]byte, 0, dataLen)
	for i := 0; i < dataShards; i++ {
		data = append(data, shards[i]...)
		if len(data) >= dataLen {
			break
		}
	}

	if len(data) > dataLen {
		data = data[:dataLen]
	}

	return data, nil
}

// RecordRTT 记录 RTT
func (af *AdaptiveFEC) RecordRTT(rtt time.Duration) {
	af.estimator.RecordRTT(rtt)
}

// GetCurrentParity 获取当前冗余分片数
func (af *AdaptiveFEC) GetCurrentParity() int {
	af.mu.RLock()
	defer af.mu.RUnlock()
	return af.currentParity
}

// GetDataShards 获取数据分片数
func (af *AdaptiveFEC) GetDataShards() int {
	return af.config.DataShards
}

// GetEstimator 获取估算器
func (af *AdaptiveFEC) GetEstimator() *LossEstimator {
	return af.estimator
}

// AdaptiveStats 自适应统计
type AdaptiveStats struct {
	CurrentParity int
	DataShards    int
	LossRate      float64
	RTT           time.Duration
	AdjustCount   int
	LastAdjust    time.Time
}

// Stats 获取统计
func (af *AdaptiveFEC) Stats() AdaptiveStats {
	af.mu.RLock()
	defer af.mu.RUnlock()

	return AdaptiveStats{
		CurrentParity: af.currentParity,
		DataShards:    af.config.DataShards,
		LossRate:      af.estimator.GetLossRate(),
		RTT:           af.estimator.GetRTT(),
		AdjustCount:   af.adjustCount,
		LastAdjust:    af.lastAdjust,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

