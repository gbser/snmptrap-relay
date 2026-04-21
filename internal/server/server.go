package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"snmptrap-relay/internal/engine"
	"snmptrap-relay/internal/model"
)

type Server struct {
	cfg    model.ServerConfig
	engine *engine.Engine
	logger *slog.Logger
	conn   *net.UDPConn
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

	buf := make([]byte, s.cfg.MaxDatagramSize)
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("shutdown_requested")
			return nil
		case now := <-ticker.C:
			s.engine.Cleanup(now)
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
		event, err := s.engine.Parse(pkt, remote.IP.String(), remote.Port)
		if err != nil {
			s.logger.Warn("trap_parse_failed",
				"source", remote.String(),
				"error", err,
			)
			continue
		}
		if _, err := s.engine.HandleEvent(event); err != nil {
			s.logger.Error("trap_handle_failed",
				"source", remote.String(),
				"error", err,
			)
		}
	}
}

func (s *Server) String() string {
	return fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
}
