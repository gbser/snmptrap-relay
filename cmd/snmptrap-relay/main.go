package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"snmptrap-relay/internal/config"
	"snmptrap-relay/internal/engine"
	"snmptrap-relay/internal/forward"
	"snmptrap-relay/internal/logging"
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

	logger, err := logging.New(cfg.Logging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logging error: %v\n", err)
		return 1
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

	relayEngine := engine.New(cfg, relayForwarder, decoder, logger)
	relayServer, err := server.New(cfg.Server, relayEngine, logger)
	if err != nil {
		logger.Error("server_init_failed", "error", err)
		return 1
	}

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
				nextLogger, err := logging.New(nextCfg.Logging)
				if err != nil {
					logger.Error("logging_reload_failed", "error", err)
					continue
				}
				nextForwarder, err := forward.NewUDP(nextCfg.Forwarders)
				if err != nil {
					logger.Error("forwarder_reload_failed", "error", err)
					continue
				}
				nextDecoder, err := receiver.New(nextCfg)
				if err != nil {
					logger.Error("decoder_reload_failed", "error", err)
					continue
				}
				oldForwarder := relayForwarder
				relayEngine.Reload(nextCfg, nextForwarder, nextDecoder, nextLogger)
				relayForwarder = nextForwarder
				logger = nextLogger
				if oldForwarder != nil {
					_ = oldForwarder.Close()
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
