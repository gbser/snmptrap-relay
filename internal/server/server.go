package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"snmptrap-relay/internal/engine"
	"snmptrap-relay/internal/model"
)

type Server struct {
	cfg        model.ServerConfig
	engine     *engine.Engine
	logger     *slog.Logger
	conn       *net.UDPConn
	stats      runtimeStats
	queueDepth atomic.Int64
}

type queuedPacket struct {
	payload []byte
	remote  *net.UDPAddr
}

type runtimeStats struct {
	received      atomic.Uint64
	queueDropped  atomic.Uint64
	parseFailed   atomic.Uint64
	handleFailed  atomic.Uint64
	accepted      atomic.Uint64
	forwarded     atomic.Uint64
	filtered      atomic.Uint64
	duplicates    atomic.Uint64
	passThrough   atomic.Uint64
	alarms        atomic.Uint64
	dedupDisabled atomic.Uint64
	forwardFailed atomic.Uint64
}

type MetricsSnapshot struct {
	Received      uint64
	QueueDropped  uint64
	QueueDepth    int64
	QueueCapacity int
	ParseFailed   uint64
	HandleFailed  uint64
	Accepted      uint64
	Forwarded     uint64
	Filtered      uint64
	Duplicates    uint64
	PassThrough   uint64
	Alarms        uint64
	DedupDisabled uint64
	ForwardFailed uint64
	WorkerCount   int
}

func New(cfg model.ServerConfig, eng *engine.Engine, logger *slog.Logger) (*Server, error) {
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, engine: eng, logger: logger, conn: conn}, nil
}

func (s *Server) Run(ctx context.Context) error {
	defer s.conn.Close()

	ticker := time.NewTicker(time.Duration(s.cfg.CleanupIntervalSeconds) * time.Second)
	defer ticker.Stop()
	var statsTicker *time.Ticker
	if s.cfg.StatsLogIntervalSecs > 0 {
		statsTicker = time.NewTicker(time.Duration(s.cfg.StatsLogIntervalSecs) * time.Second)
		defer statsTicker.Stop()
	}

	packets := make(chan queuedPacket, s.cfg.QueueSize)
	var workers sync.WaitGroup
	for i := 0; i < s.cfg.WorkerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.runWorker(ctx, packets)
		}()
	}
	defer workers.Wait()

	buf := make([]byte, s.cfg.MaxDatagramSize)
	for {
		select {
		case <-ctx.Done():
			close(packets)
			s.logger.Info("shutdown_requested")
			return nil
		case now := <-ticker.C:
			s.engine.Cleanup(now)
		case <-statsTick(statsTicker):
			s.logStats(len(packets))
		default:
		}

		_ = s.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, remote, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			return err
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		s.stats.received.Add(1)
		select {
		case packets <- queuedPacket{payload: pkt, remote: cloneUDPAddr(remote)}:
			s.queueDepth.Add(1)
		default:
			dropped := s.stats.queueDropped.Add(1)
			s.logger.Warn("trap_queue_full",
				"source", remote.String(),
				"queue_size", cap(packets),
				"queue_dropped", dropped,
			)
		}
	}
}

func (s *Server) runWorker(ctx context.Context, packets <-chan queuedPacket) {
	for {
		select {
		case <-ctx.Done():
			return
		case pkt, ok := <-packets:
			if !ok {
				return
			}
			s.queueDepth.Add(-1)
			s.processPacket(pkt)
		}
	}
}

func (s *Server) processPacket(pkt queuedPacket) {
	event, err := s.engine.Parse(pkt.payload, pkt.remote.IP.String(), pkt.remote.Port)
	if err != nil {
		s.stats.parseFailed.Add(1)
		s.logger.Warn("trap_parse_failed",
			"source", pkt.remote.String(),
			"error", err,
		)
		return
	}
	outcome, err := s.engine.HandleEvent(event)
	if err != nil {
		s.stats.handleFailed.Add(1)
		if outcome.Reason == "forward_failed" {
			s.stats.forwardFailed.Add(1)
		}
		s.logger.Error("trap_handle_failed",
			"source", pkt.remote.String(),
			"error", err,
		)
		return
	}
	s.recordOutcome(outcome)
}

func (s *Server) recordOutcome(outcome engine.Outcome) {
	if outcome.Accepted {
		s.stats.accepted.Add(1)
	}
	if outcome.Forwarded {
		s.stats.forwarded.Add(1)
	}
	switch outcome.Reason {
	case "filtered":
		s.stats.filtered.Add(1)
	case "duplicate":
		s.stats.duplicates.Add(1)
	case "pass-through":
		s.stats.passThrough.Add(1)
	case "alarm":
		s.stats.alarms.Add(1)
	case "dedup_disabled":
		s.stats.dedupDisabled.Add(1)
	case "forward_failed":
		s.stats.forwardFailed.Add(1)
	}
}

func (s *Server) logStats(queueDepth int) {
	s.logger.Info("server_stats",
		"received", s.stats.received.Load(),
		"queue_dropped", s.stats.queueDropped.Load(),
		"queue_depth", queueDepth,
		"parse_failed", s.stats.parseFailed.Load(),
		"handle_failed", s.stats.handleFailed.Load(),
		"accepted", s.stats.accepted.Load(),
		"forwarded", s.stats.forwarded.Load(),
		"filtered", s.stats.filtered.Load(),
		"duplicates", s.stats.duplicates.Load(),
		"pass_through", s.stats.passThrough.Load(),
		"alarms", s.stats.alarms.Load(),
		"dedup_disabled", s.stats.dedupDisabled.Load(),
		"forward_failed", s.stats.forwardFailed.Load(),
	)
}

func (s *Server) MetricsSnapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Received:      s.stats.received.Load(),
		QueueDropped:  s.stats.queueDropped.Load(),
		QueueDepth:    s.queueDepth.Load(),
		QueueCapacity: s.cfg.QueueSize,
		ParseFailed:   s.stats.parseFailed.Load(),
		HandleFailed:  s.stats.handleFailed.Load(),
		Accepted:      s.stats.accepted.Load(),
		Forwarded:     s.stats.forwarded.Load(),
		Filtered:      s.stats.filtered.Load(),
		Duplicates:    s.stats.duplicates.Load(),
		PassThrough:   s.stats.passThrough.Load(),
		Alarms:        s.stats.alarms.Load(),
		DedupDisabled: s.stats.dedupDisabled.Load(),
		ForwardFailed: s.stats.forwardFailed.Load(),
		WorkerCount:   s.cfg.WorkerCount,
	}
}

func statsTick(ticker *time.Ticker) <-chan time.Time {
	if ticker == nil {
		return nil
	}
	return ticker.C
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	if addr == nil {
		return nil
	}
	clone := *addr
	if addr.IP != nil {
		clone.IP = append(net.IP(nil), addr.IP...)
	}
	if addr.Zone != "" {
		clone.Zone = addr.Zone
	}
	return &clone
}

func (s *Server) String() string {
	return fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
}
