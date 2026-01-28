
package fec

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

const (
	// ShardHeaderSize 分片头大小
	// groupID(4) + index(1) + total(1) + parity(1) + reserved(1) + dataLen(2) + timestamp(4)
	ShardHeaderSize = 14
)

// ShardHeader 表示 FEC 分片的头部
type ShardHeader struct {
	GroupID   uint32
	Index     uint8
	Total     uint8
	Parity    uint8
	Reserved  uint8
	DataLen   uint16
	Timestamp uint32
}

// ParseShardHeader 从字节解析分片头
func ParseShardHeader(data []byte) (*ShardHeader, error) {
	if len(data) < ShardHeaderSize {
		return nil, fmt.Errorf("数据过短，无法解析分片头")
	}

	return &ShardHeader{
		GroupID:   binary.BigEndian.Uint32(data[0:4]),
		Index:     data[4],
		Total:     data[5],
		Parity:    data[6],
		Reserved:  data[7],
		DataLen:   binary.BigEndian.Uint16(data[8:10]),
		Timestamp: binary.BigEndian.Uint32(data[10:14]),
	}, nil
}

// Serialize 序列化分片头为字节
func (h *ShardHeader) Serialize() []byte {
	buf := make([]byte, ShardHeaderSize)
	binary.BigEndian.PutUint32(buf[0:4], h.GroupID)
	buf[4] = h.Index
	buf[5] = h.Total
	buf[6] = h.Parity
	buf[7] = h.Reserved
	binary.BigEndian.PutUint16(buf[8:10], h.DataLen)
	binary.BigEndian.PutUint32(buf[10:14], h.Timestamp)
	return buf
}

// DataShards 返回数据分片数
func (h *ShardHeader) DataShards() int {
	return int(h.Total) - int(h.Parity)
}

// ShardGroup 收集用于重建的分片
type ShardGroup struct {
	GroupID   uint32
	Shards    [][]byte
	DataLen   int
	Parity    int
	Received  int
	Created   time.Time
	Completed bool
	Timestamp uint32
}

// ShardCollector 收集并重建 FEC 分片组
type ShardCollector struct {
	groups  map[uint32]*ShardGroup
	timeout time.Duration

	totalGroups     uint64
	completedGroups uint64
	failedGroups    uint64

	mu sync.Mutex
}

// NewShardCollector 创建新的分片收集器
func NewShardCollector(timeout time.Duration) *ShardCollector {
	return &ShardCollector{
		groups:  make(map[uint32]*ShardGroup),
		timeout: timeout,
	}
}

// AddShard 添加分片并尝试重建
func (c *ShardCollector) AddShard(data []byte) ([]byte, error) {
	if len(data) < ShardHeaderSize {
		return nil, fmt.Errorf("分片过短")
	}

	header, err := ParseShardHeader(data)
	if err != nil {
		return nil, err
	}

	shardData := data[ShardHeaderSize:]

	c.mu.Lock()
	defer c.mu.Unlock()

	group, exists := c.groups[header.GroupID]
	if !exists {
		group = &ShardGroup{
			GroupID:   header.GroupID,
			Shards:    make([][]byte, header.Total),
			DataLen:   int(header.DataLen),
			Parity:    int(header.Parity),
			Created:   time.Now(),
			Timestamp: header.Timestamp,
		}
		c.groups[header.GroupID] = group
		c.totalGroups++
	}

	if group.Completed {
		return nil, nil
	}

	if int(header.Index) >= len(group.Shards) {
		return nil, fmt.Errorf("分片索引超出范围: %d >= %d", header.Index, len(group.Shards))
	}

	if group.Shards[header.Index] == nil {
		group.Shards[header.Index] = make([]byte, len(shardData))
		copy(group.Shards[header.Index], shardData)
		group.Received++
	}

	dataShards := header.DataShards()

	if group.Received >= dataShards {
		fec, err := New(dataShards, group.Parity)
		if err != nil {
			return nil, fmt.Errorf("创建解码器: %w", err)
		}
		reconstructed, err := fec.Decode(group.Shards, group.DataLen)
		if err == nil {
			group.Completed = true
			c.completedGroups++
			return reconstructed, nil
		}
	}

	return nil, nil
}

// Cleanup 删除过期的组
func (c *ShardCollector) Cleanup() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	count := 0

	for id, group := range c.groups {
		if now.Sub(group.Created) > c.timeout {
			if !group.Completed {
				c.failedGroups++
			}
			delete(c.groups, id)
			count++
		}
	}

	return count
}

// Stats 返回收集器统计信息
type CollectorStats struct {
	PendingGroups   int
	CompletedGroups uint64
	FailedGroups    uint64
	TotalGroups     uint64
}

// Stats 获取统计
func (c *ShardCollector) Stats() CollectorStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	pending := 0
	for _, group := range c.groups {
		if !group.Completed {
			pending++
		}
	}

	return CollectorStats{
		PendingGroups:   pending,
		CompletedGroups: c.completedGroups,
		FailedGroups:    c.failedGroups,
		TotalGroups:     c.totalGroups,
	}
}
