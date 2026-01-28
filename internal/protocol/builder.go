
package protocol

import (
	"sync"
	"sync/atomic"

	"github.com/anthropics/phantom-core/internal/crypto"
)

// PacketBuilder 构建加密的 Phantom 数据包
type PacketBuilder struct {
	psk        []byte
	userID     [crypto.UserIDLength]byte
	timeWindow int
	sequence   uint32

	// AEAD 缓存
	aeadCache sync.Map
}

// NewPacketBuilder 创建新的数据包构建器
func NewPacketBuilder(psk []byte, timeWindow int) *PacketBuilder {
	return &PacketBuilder{
		psk:        psk,
		userID:     crypto.DeriveUserID(psk),
		timeWindow: timeWindow,
	}
}

// GetUserID 返回用户 ID
func (b *PacketBuilder) GetUserID() [crypto.UserIDLength]byte {
	return b.userID
}

// NextSequence 返回下一个序列号
func (b *PacketBuilder) NextSequence() uint32 {
	return atomic.AddUint32(&b.sequence, 1)
}

// BuildConnectPacket 构建连接请求数据包
func (b *PacketBuilder) BuildConnectPacket(sessionID uint32, target *ConnectPayload, muxEnabled bool) ([]byte, error) {
	flags := byte(0)
	if muxEnabled {
		flags |= FlagMUX
	}

	packet := &Packet{
		Type:      PacketTypeConnect,
		SessionID: sessionID,
		StreamID:  0,
		Sequence:  b.NextSequence(),
		AckSeq:    0,
		Flags:     flags,
		Payload:   target.Serialize(),
	}

	return b.encryptPacket(packet)
}

// BuildStreamOpenPacket 构建打开流请求数据包
func (b *PacketBuilder) BuildStreamOpenPacket(sessionID, streamID uint32, target *ConnectPayload) ([]byte, error) {
	packet := &Packet{
		Type:      PacketTypeStreamOpen,
		SessionID: sessionID,
		StreamID:  streamID,
		Sequence:  b.NextSequence(),
		AckSeq:    0,
		Flags:     FlagMUX,
		Payload:   target.Serialize(),
	}

	return b.encryptPacket(packet)
}

// BuildDataPacket 构建数据包
func (b *PacketBuilder) BuildDataPacket(sessionID, streamID uint32, ackSeq uint32, data []byte, muxEnabled bool) ([]byte, error) {
	flags := byte(0)
	if muxEnabled {
		flags |= FlagMUX
	}

	packet := &Packet{
		Type:      PacketTypeData,
		SessionID: sessionID,
		StreamID:  streamID,
		Sequence:  b.NextSequence(),
		AckSeq:    ackSeq,
		Flags:     flags,
		Payload:   data,
	}

	return b.encryptPacket(packet)
}

// BuildStreamDataPacket 构建流数据包
func (b *PacketBuilder) BuildStreamDataPacket(sessionID, streamID uint32, ackSeq uint32, data []byte) ([]byte, error) {
	packet := &Packet{
		Type:      PacketTypeStreamData,
		SessionID: sessionID,
		StreamID:  streamID,
		Sequence:  b.NextSequence(),
		AckSeq:    ackSeq,
		Flags:     FlagMUX,
		Payload:   data,
	}

	return b.encryptPacket(packet)
}

// BuildClosePacket 构建关闭数据包
func (b *PacketBuilder) BuildClosePacket(sessionID uint32) ([]byte, error) {
	packet := &Packet{
		Type:      PacketTypeClose,
		SessionID: sessionID,
		StreamID:  0,
		Sequence:  b.NextSequence(),
		AckSeq:    0,
		Flags:     FlagFIN,
	}

	return b.encryptPacket(packet)
}

// BuildStreamClosePacket 构建关闭流数据包
func (b *PacketBuilder) BuildStreamClosePacket(sessionID, streamID uint32) ([]byte, error) {
	packet := &Packet{
		Type:      PacketTypeStreamClose,
		SessionID: sessionID,
		StreamID:  streamID,
		Sequence:  b.NextSequence(),
		AckSeq:    0,
		Flags:     FlagMUX | FlagFIN,
	}

	return b.encryptPacket(packet)
}

// BuildPingPacket 构建心跳请求包
func (b *PacketBuilder) BuildPingPacket(sessionID uint32, payload []byte) ([]byte, error) {
	packet := &Packet{
		Type:      PacketTypePing,
		SessionID: sessionID,
		StreamID:  0,
		Sequence:  b.NextSequence(),
		AckSeq:    0,
		Flags:     0,
		Payload:   payload,
	}

	return b.encryptPacket(packet)
}

// getAEAD 获取或创建 AEAD
func (b *PacketBuilder) getAEAD(window int64) *crypto.AEAD {
	if v, ok := b.aeadCache.Load(window); ok {
		return v.(*crypto.AEAD)
	}

	sessionKey := crypto.DeriveSessionKey(b.psk, window)
	aead, err := crypto.NewAEAD(sessionKey)
	if err != nil {
		return nil
	}

	b.aeadCache.Store(window, aead)
	return aead
}

// encryptPacket 加密数据包
func (b *PacketBuilder) encryptPacket(packet *Packet) ([]byte, error) {
	window := crypto.CurrentWindow(b.timeWindow)
	aead := b.getAEAD(window)
	if aead == nil {
		return nil, ErrCreateAEAD
	}

	plaintext := packet.Serialize()
	encrypted, err := aead.EncryptPacket(plaintext)
	if err != nil {
		return nil, err
	}

	header := &PacketHeader{
		UserID:    b.userID,
		Timestamp: crypto.TimestampLow16(),
	}

	result := make([]byte, HeaderSize+len(encrypted))
	copy(result[:HeaderSize], header.Serialize())
	copy(result[HeaderSize:], encrypted)

	return result, nil
}

// DecryptPacket 解密接收的数据包
func (b *PacketBuilder) DecryptPacket(data []byte) (*Packet, error) {
	if len(data) < HeaderSize+crypto.NonceSize+crypto.TagSize {
		return nil, ErrPacketTooShort
	}

	header, err := ParseHeader(data)
	if err != nil {
		return nil, err
	}

	encryptedPart := data[HeaderSize:]
	var plaintext []byte

	for _, window := range crypto.ValidWindows(b.timeWindow) {
		aead := b.getAEAD(window)
		if aead == nil {
			continue
		}

		plaintext, err = aead.DecryptPacket(encryptedPart)
		if err == nil {
			break
		}
	}

	if plaintext == nil {
		return nil, ErrDecryptionFailed
	}

	packet, err := ParsePacket(plaintext)
	if err != nil {
		return nil, err
	}

	packet.Header = *header
	return packet, nil
}

// CleanupAEADCache 清理过期的 AEAD 缓存
func (b *PacketBuilder) CleanupAEADCache() {
	currentWindow := crypto.CurrentWindow(b.timeWindow)

	b.aeadCache.Range(func(key, _ interface{}) bool {
		window := key.(int64)
		if currentWindow-window > 2 {
			b.aeadCache.Delete(key)
		}
		return true
	})
}

// 错误类型
var (
	ErrPacketTooShort   = &PacketError{"数据包过短"}
	ErrDecryptionFailed = &PacketError{"解密失败"}
	ErrCreateAEAD       = &PacketError{"创建 AEAD 失败"}
)

type PacketError struct {
	msg string
}

func (e *PacketError) Error() string {
	return e.msg
}

