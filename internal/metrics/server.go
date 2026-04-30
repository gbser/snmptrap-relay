package metrics

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"snmptrap-relay/internal/model"
	serverpkg "snmptrap-relay/internal/server"
)

type StatsProvider interface {
	MetricsSnapshot() serverpkg.MetricsSnapshot
}

type Server struct {
	cfg      model.MetricsConfig
	listener net.Listener
	httpSrv  *http.Server
	logger   *slog.Logger
}

const healthPath = "/healthz"

func New(cfg model.MetricsConfig, provider StatsProvider, logger *slog.Logger) (*Server, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	registry := prometheus.NewRegistry()
	registerStats(registry, provider)

	mux := http.NewServeMux()
	mux.Handle(cfg.Path, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc(healthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})

	srv := &Server{
		cfg:      cfg,
		listener: listener,
		logger:   logger,
	}
	srv.httpSrv = &http.Server{Handler: mux}

	go func() {
		if err := srv.httpSrv.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics_server_failed", "error", err)
		}
	}()

	logger.Info("metrics_server_started", "address", listener.Addr().String(), "path", cfg.Path, "health_path", healthPath)
	return srv, nil
}

func registerStats(registry *prometheus.Registry, provider StatsProvider) {
	registerCounter := func(name, help string, getter func(serverpkg.MetricsSnapshot) uint64) {
		registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help}, func() float64 {
			return float64(getter(provider.MetricsSnapshot()))
		}))
	}
	registerGauge := func(name, help string, getter func(serverpkg.MetricsSnapshot) float64) {
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, func() float64 {
			return getter(provider.MetricsSnapshot())
		}))
	}

	registerCounter("snmptrap_relay_received_total", "Total UDP traps read by the relay.", func(s serverpkg.MetricsSnapshot) uint64 { return s.Received })
	registerCounter("snmptrap_relay_queue_dropped_total", "Total traps dropped because the internal queue was full.", func(s serverpkg.MetricsSnapshot) uint64 { return s.QueueDropped })
	registerCounter("snmptrap_relay_parse_failed_total", "Total traps that failed parsing.", func(s serverpkg.MetricsSnapshot) uint64 { return s.ParseFailed })
	registerCounter("snmptrap_relay_handle_failed_total", "Total traps that failed during handling after parsing.", func(s serverpkg.MetricsSnapshot) uint64 { return s.HandleFailed })
	registerCounter("snmptrap_relay_accepted_total", "Total accepted events.", func(s serverpkg.MetricsSnapshot) uint64 { return s.Accepted })
	registerCounter("snmptrap_relay_forwarded_total", "Total forwarded events.", func(s serverpkg.MetricsSnapshot) uint64 { return s.Forwarded })
	registerCounter("snmptrap_relay_filtered_total", "Total filtered events.", func(s serverpkg.MetricsSnapshot) uint64 { return s.Filtered })
	registerCounter("snmptrap_relay_duplicates_total", "Total duplicate events suppressed by deduplication.", func(s serverpkg.MetricsSnapshot) uint64 { return s.Duplicates })
	registerCounter("snmptrap_relay_pass_through_total", "Total pass-through events forwarded without matching an alarm.", func(s serverpkg.MetricsSnapshot) uint64 { return s.PassThrough })
	registerCounter("snmptrap_relay_alarms_total", "Total alarm events forwarded as first-seen alarms.", func(s serverpkg.MetricsSnapshot) uint64 { return s.Alarms })
	registerCounter("snmptrap_relay_dedup_disabled_total", "Total events forwarded with dedup disabled due to missing key fields.", func(s serverpkg.MetricsSnapshot) uint64 { return s.DedupDisabled })
	registerCounter("snmptrap_relay_forward_failed_total", "Total events whose forwarding failed.", func(s serverpkg.MetricsSnapshot) uint64 { return s.ForwardFailed })

	registerGauge("snmptrap_relay_queue_depth", "Current number of events waiting in the internal queue.", func(s serverpkg.MetricsSnapshot) float64 { return float64(s.QueueDepth) })
	registerGauge("snmptrap_relay_queue_capacity", "Configured internal queue capacity.", func(s serverpkg.MetricsSnapshot) float64 { return float64(s.QueueCapacity) })
	registerGauge("snmptrap_relay_worker_count", "Configured worker count for trap processing.", func(s serverpkg.MetricsSnapshot) float64 { return float64(s.WorkerCount) })
}

func (s *Server) Addr() string {
	if s == nil || s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) Close() error {
	if s == nil || s.httpSrv == nil {
		return nil
	}
	if err := s.httpSrv.Close(); err != nil {
		return err
	}
	if s.logger != nil {
		s.logger.Info("metrics_server_stopped", "address", s.Addr(), "path", s.cfg.Path, "health_path", healthPath)
	}
	return nil
}

func DescribeConfig(cfg model.MetricsConfig) string {
	return fmt.Sprintf("%s:%d%s", cfg.Host, cfg.Port, cfg.Path)
}

func HealthPath() string {
	return healthPath
}
