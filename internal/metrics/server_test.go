package metrics

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"snmptrap-relay/internal/model"
	serverpkg "snmptrap-relay/internal/server"
)

type fakeProvider struct{}

func (fakeProvider) MetricsSnapshot() serverpkg.MetricsSnapshot {
	return serverpkg.MetricsSnapshot{
		Received:      10,
		QueueDropped:  2,
		QueueDepth:    3,
		QueueCapacity: 128,
		Forwarded:     7,
		WorkerCount:   2,
	}
}

func TestMetricsServerExposesPrometheusMetrics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := New(model.MetricsConfig{Enabled: true, Host: "127.0.0.1", Port: 0, Path: "/metrics"}, fakeProvider{}, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer srv.Close()

	resp, err := http.Get("http://" + srv.Addr() + "/metrics")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	out := string(body)
	if !strings.Contains(out, "snmptrap_relay_received_total 10") {
		t.Fatalf("metrics output missing received counter: %s", out)
	}
	if !strings.Contains(out, "snmptrap_relay_queue_depth 3") {
		t.Fatalf("metrics output missing queue depth gauge: %s", out)
	}
}

func TestMetricsServerExposesHealthz(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := New(model.MetricsConfig{Enabled: true, Host: "127.0.0.1", Port: 0, Path: "/metrics"}, fakeProvider{}, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer srv.Close()

	resp, err := http.Get("http://" + srv.Addr() + HealthPath())
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("health status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got, want := string(body), "ok\n"; got != want {
		t.Fatalf("health body = %q, want %q", got, want)
	}
}
