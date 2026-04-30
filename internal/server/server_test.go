package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"snmptrap-relay/internal/engine"
	"snmptrap-relay/internal/model"
)

type noopForwarder struct{}

func (n *noopForwarder) Send([]byte) error { return nil }

type slowDecoder struct {
	delay time.Duration
}

func (d *slowDecoder) Decode(data []byte, sourceIP string, sourcePort int) (*model.TrapEvent, error) {
	time.Sleep(d.delay)
	return &model.TrapEvent{
		ReceivedAt:    time.Now().UTC(),
		SourceIP:      sourceIP,
		SourcePort:    sourcePort,
		RawBytes:      append([]byte(nil), data...),
		TrapOID:       "alarm.1",
		Fields:        map[string]string{"trap_oid": "alarm.1", "source_ip": sourceIP},
		RawVarBindMap: map[string]string{},
	}, nil
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunLogsQueueFullWhenOverloaded(t *testing.T) {
	cfg := model.ServerConfig{
		Host:                   "127.0.0.1",
		Port:                   0,
		MaxDatagramSize:        1024,
		CleanupIntervalSeconds: 1,
		MaxDedupEntries:        128,
		QueueSize:              1,
		WorkerCount:            1,
		StatsLogIntervalSecs:   0,
	}
	appCfg := &model.AppConfig{Server: cfg, Filters: model.FiltersConfig{DefaultAction: "keep"}}
	logBuf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))
	eng := engine.New(appCfg, &noopForwarder{}, &slowDecoder{delay: 150 * time.Millisecond}, logger, io.Discard)
	srv, err := New(cfg, eng, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()

	addr := srv.conn.LocalAddr().(*net.UDPAddr)
	client, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		cancel()
		<-errCh
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer client.Close()

	for i := 0; i < 10; i++ {
		if _, err := client.Write([]byte("trap")); err != nil {
			cancel()
			<-errCh
			t.Fatalf("Write() error = %v", err)
		}
	}

	time.Sleep(400 * time.Millisecond)
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !strings.Contains(logBuf.String(), "trap_queue_full") {
		t.Fatalf("expected trap_queue_full log, got %s", logBuf.String())
	}
}
