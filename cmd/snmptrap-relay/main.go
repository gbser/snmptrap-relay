package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"snmptrap-relay/internal/config"
	"snmptrap-relay/internal/engine"
	"snmptrap-relay/internal/forward"
	"snmptrap-relay/internal/logging"
	"snmptrap-relay/internal/metrics"
	"snmptrap-relay/internal/receiver"
	"snmptrap-relay/internal/server"
)

func main() {
	os.Exit(run())
}

func run() int {
	var configPath string
	var checkOnly bool

	flag.StringVar(&configPath, "config", "", "path to YAML configuration file")
	flag.BoolVar(&checkOnly, "check-config", false, "load and validate config, then exit")
	flag.Parse()

	if configPath == "" {
		fmt.Fprintln(os.Stderr, "missing required --config")
		return 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	logResource, err := logging.NewResource(cfg.Logging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logging error: %v\n", err)
		return 1
	}
	logger := logResource.Logger
	loggerCloser := logResource.Closer
	if loggerCloser != nil {
		defer loggerCloser.Close()
	}
	if err := applyRuntimeConfig(cfg, logger); err != nil {
		fmt.Fprintf(os.Stderr, "runtime config error: %v\n", err)
		return 1
	}

	alertsWriter, err := logging.NewAlertsWriter(cfg.Logging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "alerts log error: %v\n", err)
		return 1
	}
	if alertsWriter != nil {
		defer alertsWriter.Close()
	}

	if checkOnly {
		validatorForwarder, err := forward.NewUDP(cfg.Forwarders)
		if err != nil {
			logger.Error("forwarder_validation_failed", "error", err)
			return 1
		}
		_ = validatorForwarder.Close()
		if _, err := receiver.New(cfg); err != nil {
			logger.Error("decoder_validation_failed", "error", err)
			return 1
		}
		if cfg.Metrics.Enabled {
			validatorServer, err := server.New(cfg.Server, engine.New(cfg, validatorForwarder, nil, logger, nil), logger)
			if err != nil {
				logger.Error("server_validation_failed", "error", err)
				return 1
			}
			validatorMetrics, err := metrics.New(cfg.Metrics, validatorServer, logger)
			if err != nil {
				logger.Error("metrics_validation_failed", "error", err)
				return 1
			}
			_ = validatorMetrics.Close()
			_ = validatorServer
		}
		logger.Info("config_ok", "path", configPath)
		return 0
	}

	relayForwarder, err := forward.NewUDP(cfg.Forwarders)
	if err != nil {
		logger.Error("forwarder_init_failed", "error", err)
		return 1
	}
	defer func() {
		_ = relayForwarder.Close()
	}()

	decoder, err := receiver.New(cfg)
	if err != nil {
		logger.Error("decoder_init_failed", "error", err)
		return 1
	}

	relayEngine := engine.New(cfg, relayForwarder, decoder, logger, alertsWriter)
	relayServer, err := server.New(cfg.Server, relayEngine, logger)
	if err != nil {
		logger.Error("server_init_failed", "error", err)
		return 1
	}

	var metricsServer *metrics.Server
	if cfg.Metrics.Enabled {
		metricsServer, err = metrics.New(cfg.Metrics, relayServer, logger)
		if err != nil {
			logger.Error("metrics_init_failed", "error", err)
			return 1
		}
		defer func() {
			_ = metricsServer.Close()
		}()
	}
	currentMetricsCfg := cfg.Metrics

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- relayServer.Run(ctx)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	for {
		select {
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGHUP:
				nextCfg, err := config.Load(configPath)
				if err != nil {
					logger.Error("config_reload_failed", "error", err)
					continue
				}
				nextLogResource, err := logging.NewResource(nextCfg.Logging)
				if err != nil {
					logger.Error("logging_reload_failed", "error", err)
					continue
				}
				nextLogger := nextLogResource.Logger
				nextLoggerCloser := nextLogResource.Closer
				if err := applyRuntimeConfig(nextCfg, nextLogger); err != nil {
					if nextLoggerCloser != nil {
						_ = nextLoggerCloser.Close()
					}
					logger.Error("runtime_reload_failed", "error", err)
					continue
				}
				nextAlertsWriter, err := logging.NewAlertsWriter(nextCfg.Logging)
				if err != nil {
					if nextLoggerCloser != nil {
						_ = nextLoggerCloser.Close()
					}
					logger.Error("alerts_log_reload_failed", "error", err)
					continue
				}
				nextForwarder, err := forward.NewUDP(nextCfg.Forwarders)
				if err != nil {
					if nextAlertsWriter != nil {
						_ = nextAlertsWriter.Close()
					}
					if nextLoggerCloser != nil {
						_ = nextLoggerCloser.Close()
					}
					logger.Error("forwarder_reload_failed", "error", err)
					continue
				}
				nextDecoder, err := receiver.New(nextCfg)
				if err != nil {
					_ = nextForwarder.Close()
					if nextAlertsWriter != nil {
						_ = nextAlertsWriter.Close()
					}
					if nextLoggerCloser != nil {
						_ = nextLoggerCloser.Close()
					}
					logger.Error("decoder_reload_failed", "error", err)
					continue
				}
				nextMetricsServer := metricsServer
				if nextCfg.Metrics != currentMetricsCfg {
					if metricsServer != nil {
						_ = metricsServer.Close()
						nextMetricsServer = nil
					}
					if nextCfg.Metrics.Enabled {
						nextMetricsServer, err = metrics.New(nextCfg.Metrics, relayServer, nextLogger)
						if err != nil {
							if currentMetricsCfg.Enabled {
								restoredMetrics, restoreErr := metrics.New(currentMetricsCfg, relayServer, logger)
								if restoreErr == nil {
									metricsServer = restoredMetrics
								} else {
									logger.Error("metrics_restore_failed", "error", restoreErr)
								}
							}
							_ = nextForwarder.Close()
							if nextAlertsWriter != nil {
								_ = nextAlertsWriter.Close()
							}
							if nextLoggerCloser != nil {
								_ = nextLoggerCloser.Close()
							}
							logger.Error("metrics_reload_failed", "error", err)
							continue
						}
					}
				}
				oldForwarder := relayForwarder
				oldLoggerCloser := loggerCloser
				oldAlertsWriter := alertsWriter
				oldMetricsServer := metricsServer
				relayEngine.Reload(nextCfg, nextForwarder, nextDecoder, nextLogger, nextAlertsWriter)
				relayForwarder = nextForwarder
				logger = nextLogger
				loggerCloser = nextLoggerCloser
				alertsWriter = nextAlertsWriter
				metricsServer = nextMetricsServer
				currentMetricsCfg = nextCfg.Metrics
				if oldLoggerCloser != nil {
					_ = oldLoggerCloser.Close()
				}
				if oldAlertsWriter != nil {
					_ = oldAlertsWriter.Close()
				}
				if oldForwarder != nil {
					_ = oldForwarder.Close()
				}
				if oldMetricsServer != nil && oldMetricsServer != metricsServer {
					_ = oldMetricsServer.Close()
				}
				logger.Info("config_reloaded", "path", configPath)
			default:
				cancel()
				if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("server_stopped_with_error", "error", err)
					return 1
				}
				return 0
			}
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("server_stopped_with_error", "error", err)
				return 1
			}
			return 0
		}
	}
}

func applyRuntimeConfig(cfg interface{ GetRuntimeMemoryLimit() string }, logger *slog.Logger) error {
	value := cfg.GetRuntimeMemoryLimit()
	if value == "" {
		return nil
	}
	limit, err := config.ParseMemoryLimit(value)
	if err != nil {
		return err
	}
	debug.SetMemoryLimit(limit)
	logger.Info("runtime_memory_limit_configured", "memory_limit", value, "bytes", limit)
	return nil
}
