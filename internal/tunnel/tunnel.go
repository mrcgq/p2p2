// internal/tunnel/tunnel.go

package tunnel

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropics/phantom-core/internal/buffer"
	"github.com/anthropics/phantom-core/internal/config"
	"github.com/anthropics/phantom-core/internal/crypto"
	"github.com/anthropics/phantom-core/internal/fec"
	"github.com/anthropics/phantom-core/internal/logger"
	"github.com/anthropics/phantom-core/internal/protocol"
	"github.com/anthropics/phantom-core/internal/stats"
	"github.com/anthropics/phantom-core/internal/transport"
)

// Tunnel 管理到 Phantom 服务器的连接
type Tunnel struct {
	config    *config.Config
	transport transport.Transport
	builder   *protocol.PacketBuilder

	// FEC
	fecEncoder   *fec.FECEncoder
	fecCollector *fec.ShardCollector

	// 多路复用
	mux        *Mux
	muxEnabled bool

	// 非多路复用模式的会话管理
	sessions *SessionManager

	// 速率限制
	rateLimiter *buffer.TokenBucket

	// 统计
	stats *stats.Collector
	log   *logger.Logger

	// 状态
	connected atomic.Bool
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
}

// New 创建新隧道
func New(cfg *config.Config, statsCollector *stats.Collector, log *logger.Logger) (*Tunnel, error) {
	psk, err := crypto.DecodePSK(cfg.Server.PSK)
	if err != nil {
		return nil, fmt.Errorf("解码 PSK: %w", err)
	}

	builder := protocol.NewPacketBuilder(psk, cfg.Server.TimeWindow)

	var fecEncoder *fec.FECEncoder
	var fecCollector *fec.ShardCollector
	if cfg.FEC.Enabled {
		if cfg.FEC.Mode == "adaptive" {
			adaptiveConfig := fec.AdaptiveConfig{
				DataShards:        cfg.FEC.DataShards,
				MinParity:         cfg.FEC.MinParity,
				MaxParity:         cfg.FEC.MaxParity,
				InitialParity:     cfg.FEC.FECShards,
				TargetLossRate:    cfg.FEC.TargetLoss,
				AdjustInterval:    cfg.FEC.AdjustInterval,
				IncreaseThreshold: 0.05,
				DecreaseThreshold: 0.01,
				StepUp:            1,
				StepDown:          1,
			}
			fecEncoder, err = fec.NewAdaptiveFECEncoder(adaptiveConfig)
			if err != nil {
				return nil, fmt.Errorf("创建自适应 FEC: %w", err)
			}
			log.Info("Adaptive FEC 已启用: %d 数据分片, %d-%d 冗余分片",
				cfg.FEC.DataShards, cfg.FEC.MinParity, cfg.FEC.MaxParity)
		} else {
			fecEncoder, err = fec.NewFECEncoder(cfg.FEC.DataShards, cfg.FEC.FECShards)
			if err != nil {
				return nil, fmt.Errorf("创建静态 FEC: %w", err)
			}
			log.Info("Static FEC 已启用: %d 数据分片 + %d 冗余分片",
				cfg.FEC.DataShards, cfg.FEC.FECShards)
		}
		fecCollector = fec.NewShardCollector(5 * time.Second)
	}

	rateLimiter := buffer.NewTokenBucket(
		float64(cfg.Buffer.MaxBurstSize),
		10*1024*1024,
	)

	return &Tunnel{
		config:       cfg,
		builder:      builder,
		fecEncoder:   fecEncoder,
		fecCollector: fecCollector,
		sessions:     NewSessionManager(),
		muxEnabled:   cfg.Mux.Enabled,
		rateLimiter:  rateLimiter,
		stats:        statsCollector,
		log:          log,
	}, nil
}

// Connect 建立与服务器的连接
func (t *Tunnel) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.connected.Load() {
		return nil
	}

	transportCfg := &transport.Config{
		Address:       t.config.Server.Address,
		TCPPort:       t.config.Server.TCPPort,
		UDPPort:       t.config.Server.UDPPort,
		TLSEnabled:    t.config.Server.TLS.Enabled,
		TLSServerName: t.config.Server.TLS.ServerName,
		TLSSkipVerify: t.config.Server.TLS.SkipVerify,
	}

	if t.config.Server.Mode == "tcp" {
		t.transport = transport.NewTCPTransport(transportCfg)
	} else {
		t.transport = transport.NewUDPTransport(transportCfg)
	}

	if err := t.transport.Dial(ctx); err != nil {
		return err
	}

	t.ctx, t.cancel = context.WithCancel(ctx)

	// 启动 FEC
	if t.fecEncoder != nil {
		t.fecEncoder.Start(t.ctx)
	}

	// 如果启用多路复用，创建 Mux 会话
	if t.muxEnabled {
		muxConfig := MuxConfig{
			MaxStreams:        t.config.Mux.MaxStreams,
			StreamBufferSize:  t.config.Mux.StreamBuffer,
			KeepAliveInterval: t.config.Mux.KeepAliveInterval,
			IdleTimeout:       t.config.Mux.IdleTimeout,
		}
		t.mux = NewMux(1, muxConfig)

		// 发送多路复用连接请求
		if err := t.sendMuxConnect(); err != nil {
			t.transport.Close()
			return fmt.Errorf("发送多路复用连接请求: %w", err)
		}
	}

	// 启动接收器
	t.wg.Add(1)
	go t.receiveLoop()

	// 启动 FEC 清理
	if t.fecCollector != nil {
		t.wg.Add(1)
		go t.fecCleanupLoop()
	}

	// 启动心跳
	if t.muxEnabled && t.mux != nil {
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.mux.StartKeepalive(t.ctx, t.sendPing)
		}()
	}

	// 启动统计更新
	t.wg.Add(1)
	go t.statsUpdateLoop()

	t.connected.Store(true)
	t.log.Info("已连接到 %s:%d (%s, Mux: %v)",
		t.config.Server.Address,
		t.getServerPort(),
		t.config.Server.Mode,
		t.muxEnabled)

	return nil
}

func (t *Tunnel) getServerPort() int {
	if t.config.Server.Mode == "tcp" {
		return t.config.Server.TCPPort
	}
	return t.config.Server.UDPPort
}

// sendMuxConnect 发送多路复用连接请求
func (t *Tunnel) sendMuxConnect() error {
	target := &protocol.ConnectPayload{
		Network:  protocol.NetworkTCP,
		AddrType: protocol.AddrTypeDomain,
		Address:  "mux",
		Port:     0,
	}

	packet, err := t.builder.BuildConnectPacket(t.mux.SessionID(), target, true)
	if err != nil {
		return err
	}

	return t.sendPacket(packet)
}

// sendPing 发送心跳
func (t *Tunnel) sendPing() error {
	timestamp := make([]byte, 8)
	now := time.Now().UnixNano()
	for i := 0; i < 8; i++ {
		timestamp[i] = byte(now >> (56 - i*8))
	}

	packet, err := t.builder.BuildPingPacket(t.mux.SessionID(), timestamp)
	if err != nil {
		return err
	}

	return t.sendPacket(packet)
}

// Disconnect 关闭连接
func (t *Tunnel) Disconnect() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected.Load() {
		return
	}

	t.cancel()
	t.transport.Close()
	t.wg.Wait()
	t.connected.Store(false)

	if t.mux != nil {
		t.mux.Close()
		t.mux = nil
	}

	t.sessions.CloseAll()

	t.log.Info("已断开连接")
}

// IsConnected 返回连接状态
func (t *Tunnel) IsConnected() bool {
	return t.connected.Load()
}

// OpenStream 打开新流（多路复用模式）
func (t *Tunnel) OpenStream(network, address string, port uint16) (*Stream, error) {
	if !t.connected.Load() {
		return nil, fmt.Errorf("未连接")
	}

	if !t.muxEnabled || t.mux == nil {
		return nil, fmt.Errorf("未启用多路复用")
	}

	targetAddr := fmt.Sprintf("%s:%d", address, port)
	stream, err := t.mux.OpenStream(network, targetAddr)
	if err != nil {
		return nil, err
	}

	// 构建目标信息
	var addrType byte = protocol.AddrTypeDomain
	if ip := net.ParseIP(address); ip != nil {
		if ip.To4() != nil {
			addrType = protocol.AddrTypeIPv4
		} else {
			addrType = protocol.AddrTypeIPv6
		}
	}

	var networkByte byte = protocol.NetworkTCP
	if network == "udp" {
		networkByte = protocol.NetworkUDP
	}

	target := &protocol.ConnectPayload{
		Network:  networkByte,
		AddrType: addrType,
		Address:  address,
		Port:     port,
	}

	// 发送打开流请求
	packet, err := t.builder.BuildStreamOpenPacket(t.mux.SessionID(), stream.ID(), target)
	if err != nil {
		t.mux.CloseStream(stream.ID())
		return nil, err
	}

	if err := t.sendPacket(packet); err != nil {
		t.mux.CloseStream(stream.ID())
		return nil, err
	}

	t.log.Debug("流 %d 已打开: %s %s", stream.ID(), network, targetAddr)
	t.stats.AddConnection()

	return stream, nil
}

// OpenSession 打开新会话（非多路复用模式）
func (t *Tunnel) OpenSession(network, address string, port uint16) (*Session, error) {
	if !t.connected.Load() {
		return nil, fmt.Errorf("未连接")
	}

	if t.muxEnabled {
		return nil, fmt.Errorf("多路复用模式下请使用 OpenStream")
	}

	session := t.sessions.Create(t.config.Buffer.SendBuffer, t.config.Buffer.RecvBuffer)
	session.SetTarget(network, fmt.Sprintf("%s:%d", address, port))

	var addrType byte = protocol.AddrTypeDomain
	if ip := net.ParseIP(address); ip != nil {
		if ip.To4() != nil {
			addrType = protocol.AddrTypeIPv4
		} else {
			addrType = protocol.AddrTypeIPv6
		}
	}

	var networkByte byte = protocol.NetworkTCP
	if network == "udp" {
		networkByte = protocol.NetworkUDP
	}

	target := &protocol.ConnectPayload{
		Network:  networkByte,
		AddrType: addrType,
		Address:  address,
		Port:     port,
	}

	packet, err := t.builder.BuildConnectPacket(session.ID, target, false)
	if err != nil {
		t.sessions.Delete(session.ID)
		return nil, err
	}

	if err := t.sendPacket(packet); err != nil {
		t.sessions.Delete(session.ID)
		return nil, err
	}

	t.log.Debug("会话 %d 已打开: %s %s:%d", session.ID, network, address, port)
	t.stats.AddConnection()

	return session, nil
}

// SendStreamData 发送流数据
func (t *Tunnel) SendStreamData(streamID uint32, data []byte) error {
	if !t.connected.Load() || t.mux == nil {
		return fmt.Errorf("未连接")
	}

	stream, ok := t.mux.GetStream(streamID)
	if !ok {
		return fmt.Errorf("流未找到")
	}

	t.rateLimiter.TakeWait(float64(len(data)))

	packet, err := t.builder.BuildStreamDataPacket(
		t.mux.SessionID(),
		streamID,
		stream.GetRecvSeq(),
		data,
	)
	if err != nil {
		return err
	}

	if err := t.sendPacket(packet); err != nil {
		return err
	}

	t.stats.AddUpload(int64(len(data)))
	return nil
}

// SendData 发送会话数据（非多路复用模式）
func (t *Tunnel) SendData(sessionID uint32, data []byte) error {
	if !t.connected.Load() {
		return fmt.Errorf("未连接")
	}

	session := t.sessions.Get(sessionID)
	if session == nil {
		return fmt.Errorf("会话未找到")
	}

	t.rateLimiter.TakeWait(float64(len(data)))

	packet, err := t.builder.BuildDataPacket(sessionID, 0, session.GetAckSeq(), data, false)
	if err != nil {
		return err
	}

	if err := t.sendPacket(packet); err != nil {
		return err
	}

	t.stats.AddUpload(int64(len(data)))
	return nil
}

// CloseStream 关闭流
func (t *Tunnel) CloseStream(streamID uint32) error {
	if t.mux == nil {
		return nil
	}

	packet, _ := t.builder.BuildStreamClosePacket(t.mux.SessionID(), streamID)
	t.sendPacket(packet)

	t.mux.CloseStream(streamID)
	t.stats.RemoveConnection()
	t.log.Debug("流 %d 已关闭", streamID)
	return nil
}

// CloseSession 关闭会话
func (t *Tunnel) CloseSession(sessionID uint32) error {
	session := t.sessions.Get(sessionID)
	if session == nil {
		return nil
	}

	packet, _ := t.builder.BuildClosePacket(sessionID)
	t.sendPacket(packet)

	t.sessions.Delete(sessionID)
	t.stats.RemoveConnection()
	t.log.Debug("会话 %d 已关闭", sessionID)
	return nil
}

// sendPacket 发送数据包（可选 FEC）
func (t *Tunnel) sendPacket(data []byte) error {
	if t.fecEncoder != nil {
		shards, err := t.fecEncoder.Encode(data)
		if err != nil {
			return err
		}

		for _, shard := range shards {
			if err := t.transport.Send(shard); err != nil {
				return err
			}
		}
	} else {
		if err := t.transport.Send(data); err != nil {
			return err
		}
	}

	return nil
}

// receiveLoop 接收数据循环
func (t *Tunnel) receiveLoop() {
	defer t.wg.Done()

	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		data, err := t.transport.Receive()
		if err != nil {
			select {
			case <-t.ctx.Done():
				return
			default:
				t.log.Debug("接收错误: %v", err)
				continue
			}
		}

		t.handleReceived(data)
	}
}

// handleReceived 处理接收的数据
func (t *Tunnel) handleReceived(data []byte) {
	var processData []byte

	if t.fecCollector != nil {
		reconstructed, err := t.fecCollector.AddShard(data)
		if err != nil {
			processData = data
		} else if reconstructed != nil {
			processData = reconstructed
			t.stats.RecordFECRecovered()
		} else {
			return
		}
	} else {
		processData = data
	}

	packet, err := t.builder.DecryptPacket(processData)
	if err != nil {
		t.log.Debug("解密错误: %v", err)
		return
	}

	switch packet.Type {
	case protocol.PacketTypeConnectAck:
		t.handleConnectAck(packet)
	case protocol.PacketTypeData, protocol.PacketTypeDataAck:
		t.handleData(packet)
	case protocol.PacketTypeStreamData:
		t.handleStreamData(packet)
	case protocol.PacketTypeStreamAck:
		t.handleStreamAck(packet)
	case protocol.PacketTypeClose, protocol.PacketTypeCloseAck:
		t.handleClose(packet)
	case protocol.PacketTypeStreamClose:
		t.handleStreamClose(packet)
	case protocol.PacketTypePong:
		t.handlePong(packet)
	}
}

// handleConnectAck 处理连接确认
func (t *Tunnel) handleConnectAck(packet *protocol.Packet) {
	if packet.IsMux() {
		t.log.Debug("多路复用会话已确认")
		return
	}

	session := t.sessions.Get(packet.SessionID)
	if session != nil {
		session.SetConnected()
		t.log.Debug("会话 %d 已连接", packet.SessionID)
	}
}

// handleData 处理数据包（非多路复用）
func (t *Tunnel) handleData(packet *protocol.Packet) {
	session := t.sessions.Get(packet.SessionID)
	if session == nil {
		return
	}

	session.UpdateAckSeq(packet.Sequence)

	if len(packet.Payload) > 0 {
		session.WriteRecv(packet.Payload)
		t.stats.AddDownload(int64(len(packet.Payload)))
	}
}

// handleStreamData 处理流数据
func (t *Tunnel) handleStreamData(packet *protocol.Packet) {
	if t.mux == nil {
		return
	}

	stream, ok := t.mux.GetStream(packet.StreamID)
	if !ok {
		return
	}

	stream.UpdateRecvSeq(packet.Sequence)

	if len(packet.Payload) > 0 {
		stream.PushData(packet.Payload)
		t.stats.AddDownload(int64(len(packet.Payload)))
	}
}

// handleStreamAck 处理流确认
func (t *Tunnel) handleStreamAck(packet *protocol.Packet) {
	if t.mux == nil {
		return
	}

	stream, ok := t.mux.GetStream(packet.StreamID)
	if !ok {
		return
	}

	stream.Touch()
	t.log.Debug("流 %d 已确认", packet.StreamID)
}

// handleClose 处理关闭
func (t *Tunnel) handleClose(packet *protocol.Packet) {
	t.sessions.Delete(packet.SessionID)
	t.stats.RemoveConnection()
	t.log.Debug("会话 %d 被服务器关闭", packet.SessionID)
}

// handleStreamClose 处理流关闭
func (t *Tunnel) handleStreamClose(packet *protocol.Packet) {
	if t.mux != nil {
		t.mux.CloseStream(packet.StreamID)
		t.stats.RemoveConnection()
		t.log.Debug("流 %d 被服务器关闭", packet.StreamID)
	}
}

// handlePong 处理心跳响应
func (t *Tunnel) handlePong(packet *protocol.Packet) {
	if t.mux != nil {
		t.mux.OnPong()

		// 计算 RTT
		if len(packet.Payload) >= 8 {
			var sentTime int64
			for i := 0; i < 8; i++ {
				sentTime |= int64(packet.Payload[i]) << (56 - i*8)
			}
			rtt := time.Duration(time.Now().UnixNano() - sentTime)
			if t.fecEncoder != nil {
				t.fecEncoder.RecordRTT(rtt)
			}
		}
	}
}

// fecCleanupLoop FEC 清理循环
func (t *Tunnel) fecCleanupLoop() {
	defer t.wg.Done()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
			t.fecCollector.Cleanup()
		}
	}
}

// statsUpdateLoop 统计更新循环
func (t *Tunnel) statsUpdateLoop() {
	defer t.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
			if t.fecEncoder != nil {
				t.stats.SetCurrentParity(int32(t.fecEncoder.GetCurrentParity()))
				t.stats.SetLossRate(t.fecEncoder.GetLossRate())
			}
			t.builder.CleanupAEADCache()
		}
	}
}

// GetMux 返回多路复用器
func (t *Tunnel) GetMux() *Mux {
	return t.mux
}

// GetSessions 返回会话管理器
func (t *Tunnel) GetSessions() *SessionManager {
	return t.sessions
}

// IsMuxEnabled 返回是否启用多路复用
func (t *Tunnel) IsMuxEnabled() bool {
	return t.muxEnabled
}
