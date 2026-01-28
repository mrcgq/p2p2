
package fec

import (
	"fmt"
	"sync"

	"github.com/klauspost/reedsolomon"
)

// FEC 实现 Reed-Solomon 前向纠错
type FEC struct {
	dataShards   int
	parityShards int
	encoder      reedsolomon.Encoder
	mu           sync.RWMutex
}

// New 创建新的 FEC 编码器/解码器
func New(dataShards, parityShards int) (*FEC, error) {
	if dataShards <= 0 || parityShards <= 0 {
		return nil, fmt.Errorf("分片数必须为正数")
	}
	if dataShards+parityShards > 256 {
		return nil, fmt.Errorf("分片总数不能超过 256")
	}

	encoder, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return nil, fmt.Errorf("创建 RS 编码器: %w", err)
	}

	return &FEC{
		dataShards:   dataShards,
		parityShards: parityShards,
		encoder:      encoder,
	}, nil
}

// TotalShards 返回分片总数
func (f *FEC) TotalShards() int {
	return f.dataShards + f.parityShards
}

// DataShards 返回数据分片数
func (f *FEC) DataShards() int {
	return f.dataShards
}

// ParityShards 返回冗余分片数
func (f *FEC) ParityShards() int {
	return f.parityShards
}

// Encode 将数据编码为数据分片 + 冗余分片
func (f *FEC) Encode(data []byte) ([][]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("数据不能为空")
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	shardSize := (len(data) + f.dataShards - 1) / f.dataShards

	shards := make([][]byte, f.TotalShards())
	for i := 0; i < f.TotalShards(); i++ {
		shards[i] = make([]byte, shardSize)
	}

	offset := 0
	for i := 0; i < f.dataShards; i++ {
		if offset >= len(data) {
			break
		}
		n := copy(shards[i], data[offset:])
		offset += n
	}

	if err := f.encoder.Encode(shards); err != nil {
		return nil, fmt.Errorf("编码分片: %w", err)
	}

	return shards, nil
}

// Decode 从分片重建数据
func (f *FEC) Decode(shards [][]byte, dataLen int) ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if len(shards) != f.TotalShards() {
		return nil, fmt.Errorf("分片数量错误: 期望 %d, 实际 %d",
			f.TotalShards(), len(shards))
	}

	available := 0
	for _, shard := range shards {
		if shard != nil && len(shard) > 0 {
			available++
		}
	}

	if available < f.dataShards {
		return nil, fmt.Errorf("分片不足: 需要 %d, 可用 %d",
			f.dataShards, available)
	}

	if err := f.encoder.Reconstruct(shards); err != nil {
		return nil, fmt.Errorf("重建分片: %w", err)
	}

	ok, err := f.encoder.Verify(shards)
	if err != nil {
		return nil, fmt.Errorf("验证分片: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("分片验证失败")
	}

	data := make([]byte, 0, dataLen)
	for i := 0; i < f.dataShards; i++ {
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

// CanRecover 检查给定的分片是否足以恢复数据
func (f *FEC) CanRecover(shards [][]byte) bool {
	available := 0
	for _, shard := range shards {
		if shard != nil && len(shard) > 0 {
			available++
		}
	}
	return available >= f.dataShards
}

