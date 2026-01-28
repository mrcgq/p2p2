
package fec

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// FECEncoder 封装带有数据包帧的 FEC（支持自适应和静态模式）
type FECEncoder struct {
	adaptiveFEC *AdaptiveFEC
	staticFEC   *FEC
	useAdaptive bool

	groupID uint32
	mu      sync.Mutex
}

// NewFECEncoder 创建静态模式 FEC 编码器
func NewFECEncoder(dataShards, parityShards int) (*FECEncoder, error) {
	fec, err := New(dataShards, parityShards)
	if err != nil {
		return nil, err
	}
	return &FECEncoder{staticFEC: fec}, nil
}

// NewAdaptiveFECEncoder 创建自适应模式 FEC 编码器
func NewAdaptiveFECEncoder(config AdaptiveConfig) (*FECEncoder, error) {
	af, err := NewAdaptiveFEC(config)
	if err != nil {
		return nil, err
	}
	return &FECEncoder{
		adaptiveFEC: af,
		useAdaptive: true,
	}, nil
}

// Encode 将数据编码为带头部的多个分片
func (e *FECEncoder) Encode(data []byte) ([][]byte, error) {
	e.mu.Lock()
	groupID := atomic.AddUint32(&e.groupID, 1)
	e.mu.Unlock()

	var shards [][]byte
	var parity int
	var err error

	if e.useAdaptive && e.adaptiveFEC != nil {
		shards, parity, err = e.adaptiveFEC.Encode(data)
	} else if e.staticFEC != nil {
		shards, err = e.staticFEC.Encode(data)
		parity = e.staticFEC.ParityShards()
	} else {
		return nil, ErrFECNotInitialized
	}

	if err != nil {
		return nil, err
	}

	timestamp := uint32(time.Now().Unix() & 0xFFFFFFFF)
	result := make([][]byte, len(shards))

	for i, shard := range shards {
		header := &ShardHeader{
			GroupID:   groupID,
			Index:     uint8(i),
			Total:     uint8(len(shards)),
			Parity:    uint8(parity),
			DataLen:   uint16(len(data)),
			Timestamp: timestamp,
		}
		result[i] = append(header.Serialize(), shard...)
	}

	return result, nil
}

// Start 启动自适应调整（如果使用自适应模式）
func (e *FECEncoder) Start(ctx context.Context) {
	if e.useAdaptive && e.adaptiveFEC != nil {
		e.adaptiveFEC.Start(ctx)
	}
}

// RecordRTT 记录 RTT
func (e *FECEncoder) RecordRTT(rtt time.Duration) {
	if e.useAdaptive && e.adaptiveFEC != nil {
		e.adaptiveFEC.RecordRTT(rtt)
	}
}

// GetCurrentParity 获取当前冗余分片数
func (e *FECEncoder) GetCurrentParity() int {
	if e.useAdaptive && e.adaptiveFEC != nil {
		return e.adaptiveFEC.GetCurrentParity()
	}
	if e.staticFEC != nil {
		return e.staticFEC.ParityShards()
	}
	return 0
}

// GetDataShards 获取数据分片数
func (e *FECEncoder) GetDataShards() int {
	if e.useAdaptive && e.adaptiveFEC != nil {
		return e.adaptiveFEC.GetDataShards()
	}
	if e.staticFEC != nil {
		return e.staticFEC.DataShards()
	}
	return 0
}

// IsAdaptive 是否为自适应模式
func (e *FECEncoder) IsAdaptive() bool {
	return e.useAdaptive
}

// GetLossRate 获取丢包率
func (e *FECEncoder) GetLossRate() float64 {
	if e.useAdaptive && e.adaptiveFEC != nil {
		return e.adaptiveFEC.GetEstimator().GetLossRate()
	}
	return 0
}

// Stats 获取统计
func (e *FECEncoder) Stats() AdaptiveStats {
	if e.useAdaptive && e.adaptiveFEC != nil {
		return e.adaptiveFEC.Stats()
	}
	return AdaptiveStats{
		DataShards:    e.GetDataShards(),
		CurrentParity: e.GetCurrentParity(),
	}
}

// 错误类型
var ErrFECNotInitialized = &FECError{"FEC 未初始化"}

type FECError struct {
	msg string
}

func (e *FECError) Error() string {
	return e.msg
}


