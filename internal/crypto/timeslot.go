
package crypto

import (
	"time"
)

// CurrentWindow 返回当前时间窗口索引
func CurrentWindow(windowSize int) int64 {
	return time.Now().Unix() / int64(windowSize)
}

// ValidWindows 返回用于验证的有效时间窗口列表
func ValidWindows(windowSize int) []int64 {
	current := CurrentWindow(windowSize)
	return []int64{
		current - 1,
		current,
		current + 1,
	}
}

// TimestampLow16 返回当前时间戳的低 16 位
func TimestampLow16() uint16 {
	return uint16(time.Now().Unix() & 0xFFFF)
}

// TimestampLow32 返回当前时间戳的低 32 位
func TimestampLow32() uint32 {
	return uint32(time.Now().Unix() & 0xFFFFFFFF)
}

// ValidateTimestamp 检查时间戳是否在可接受范围内
func ValidateTimestamp(ts uint16, windowSize int) bool {
	current := TimestampLow16()
	diff := int(current) - int(ts)

	if diff < -32768 {
		diff += 65536
	} else if diff > 32768 {
		diff -= 65536
	}

	if diff < 0 {
		diff = -diff
	}

	return diff <= windowSize*2
}

