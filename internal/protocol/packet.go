
package protocol

import (
	"encoding/binary"
	"fmt"
	"net"
)

const (
	// 数据包类型 - 与服务端保持一致
	PacketTypeConnect    = 0x01
	PacketTypeConnectAck = 0x02
	PacketTypeData       = 0x03
	PacketTypeDataAck    = 0x04
	PacketTypeClose      = 0x05
	PacketTypeCloseAck   = 0x06
	PacketTypePing       = 0x07
	PacketTypePong       = 0x08

	// 多路复用相关
	PacketTypeStreamOpen  = 0x10
	PacketTypeStreamData  = 0x11
	PacketTypeStreamClose = 0x12
	PacketTypeStreamAck   = 0x13

	// 地址类型
	AddrTypeIPv4   = 0x01
	AddrTypeIPv6   = 0x04
	AddrTypeDomain = 0x03

	// 网络类型
	NetworkTCP = 0x01
	NetworkUDP = 0x02

	// 头部大小
	UserIDSize    = 4
	TimestampSize = 2
	HeaderSize    = UserIDSize + TimestampSize

	// 限制
	MaxPacketSize  = 65535
	MaxPayloadSize = 65000
	MaxDomainLen   = 255
)

// PacketFlags 数据包标志
const (
	FlagFIN  = 0x01
	FlagSYN  = 0x02
	FlagACK  = 0x04
	FlagPSH  = 0x08
	FlagURG  = 0x10
	FlagMUX  = 0x20
	FlagFEC  = 0x40
	FlagComp = 0x80
)

// PacketHeader 数据包头（明文部分）
type PacketHeader struct {
	UserID    [UserIDSize]byte
	Timestamp uint16
}

// Packet 解密后的 Phantom 数据包
type Packet struct {
	Header    PacketHeader
	Type      byte
	SessionID uint32
	StreamID  uint32
	Sequence  uint32
	AckSeq    uint32
	Flags     byte
	Payload   []byte
}

// ParseHeader 从原始字节解析数据包头
func ParseHeader(data []byte) (*PacketHeader, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("数据包过短，无法解析头部")
	}

	header := &PacketHeader{
		Timestamp: binary.BigEndian.Uint16(data[UserIDSize:]),
	}
	copy(header.UserID[:], data[:UserIDSize])

	return header, nil
}

// Serialize 序列化数据包头为字节
func (h *PacketHeader) Serialize() []byte {
	buf := make([]byte, HeaderSize)
	copy(buf[:UserIDSize], h.UserID[:])
	binary.BigEndian.PutUint16(buf[UserIDSize:], h.Timestamp)
	return buf
}

// ParsePacket 解析解密后的数据包载荷
func ParsePacket(data []byte) (*Packet, error) {
	// 最小长度: type(1) + sessionID(4) + streamID(4) + sequence(4) + ackSeq(4) + flags(1) = 18
	if len(data) < 18 {
		return nil, fmt.Errorf("数据包过短: %d 字节", len(data))
	}

	p := &Packet{
		Type:      data[0],
		SessionID: binary.BigEndian.Uint32(data[1:5]),
		StreamID:  binary.BigEndian.Uint32(data[5:9]),
		Sequence:  binary.BigEndian.Uint32(data[9:13]),
		AckSeq:    binary.BigEndian.Uint32(data[13:17]),
		Flags:     data[17],
		Payload:   data[18:],
	}

	return p, nil
}

// Serialize 序列化数据包为字节（用于加密）
func (p *Packet) Serialize() []byte {
	buf := make([]byte, 18+len(p.Payload))
	buf[0] = p.Type
	binary.BigEndian.PutUint32(buf[1:5], p.SessionID)
	binary.BigEndian.PutUint32(buf[5:9], p.StreamID)
	binary.BigEndian.PutUint32(buf[9:13], p.Sequence)
	binary.BigEndian.PutUint32(buf[13:17], p.AckSeq)
	buf[17] = p.Flags
	copy(buf[18:], p.Payload)
	return buf
}

// IsMux 是否为多路复用数据包
func (p *Packet) IsMux() bool {
	return p.Flags&FlagMUX != 0
}

// IsFEC 是否启用 FEC
func (p *Packet) IsFEC() bool {
	return p.Flags&FlagFEC != 0
}

// HasACK 是否包含确认
func (p *Packet) HasACK() bool {
	return p.Flags&FlagACK != 0
}

// IsFIN 是否为结束包
func (p *Packet) IsFIN() bool {
	return p.Flags&FlagFIN != 0
}

// ConnectPayload 连接请求载荷
type ConnectPayload struct {
	Network  byte
	AddrType byte
	Address  string
	Port     uint16
}

// ParseConnectPayload 解析连接请求载荷
func ParseConnectPayload(data []byte) (*ConnectPayload, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("连接载荷过短")
	}

	c := &ConnectPayload{
		Network:  data[0],
		AddrType: data[1],
	}

	offset := 2

	switch c.AddrType {
	case AddrTypeIPv4:
		if len(data) < offset+4+2 {
			return nil, fmt.Errorf("IPv4 地址无效")
		}
		c.Address = net.IP(data[offset : offset+4]).String()
		offset += 4

	case AddrTypeIPv6:
		if len(data) < offset+16+2 {
			return nil, fmt.Errorf("IPv6 地址无效")
		}
		c.Address = net.IP(data[offset : offset+16]).String()
		offset += 16

	case AddrTypeDomain:
		if len(data) < offset+1 {
			return nil, fmt.Errorf("域名长度无效")
		}
		domainLen := int(data[offset])
		offset++
		if domainLen == 0 || domainLen > MaxDomainLen {
			return nil, fmt.Errorf("域名长度无效: %d", domainLen)
		}
		if len(data) < offset+domainLen+2 {
			return nil, fmt.Errorf("域名数据不完整")
		}
		c.Address = string(data[offset : offset+domainLen])
		offset += domainLen

	default:
		return nil, fmt.Errorf("未知地址类型: %d", c.AddrType)
	}

	if len(data) < offset+2 {
		return nil, fmt.Errorf("缺少端口数据")
	}
	c.Port = binary.BigEndian.Uint16(data[offset:])

	return c, nil
}

// Serialize 序列化连接请求
func (c *ConnectPayload) Serialize() []byte {
	var buf []byte

	buf = append(buf, c.Network)
	buf = append(buf, c.AddrType)

	switch c.AddrType {
	case AddrTypeIPv4:
		ip := net.ParseIP(c.Address).To4()
		if ip == nil {
			ip = make([]byte, 4)
		}
		buf = append(buf, ip...)
	case AddrTypeIPv6:
		ip := net.ParseIP(c.Address).To16()
		if ip == nil {
			ip = make([]byte, 16)
		}
		buf = append(buf, ip...)
	case AddrTypeDomain:
		buf = append(buf, byte(len(c.Address)))
		buf = append(buf, []byte(c.Address)...)
	}

	portBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(portBuf, c.Port)
	buf = append(buf, portBuf...)

	return buf
}

// NetworkString 返回网络类型字符串
func (c *ConnectPayload) NetworkString() string {
	switch c.Network {
	case NetworkTCP:
		return "tcp"
	case NetworkUDP:
		return "udp"
	default:
		return "unknown"
	}
}

// String 返回地址字符串
func (c *ConnectPayload) String() string {
	return fmt.Sprintf("%s://%s:%d", c.NetworkString(), c.Address, c.Port)
}

