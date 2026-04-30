package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAppliesDefaultMaxDedupEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := strings.Join([]string{
		"server:",
		"  host: 127.0.0.1",
		"  port: 9161",
		"dedup_defaults:",
		"  key_fields:",
		"    - trap_oid",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Server.MaxDedupEntries, 10000; got != want {
		t.Fatalf("MaxDedupEntries = %d, want %d", got, want)
	}
	if got, want := cfg.Server.QueueSize, 1024; got != want {
		t.Fatalf("QueueSize = %d, want %d", got, want)
	}
	if got, want := cfg.Server.WorkerCount, 1; got != want {
		t.Fatalf("WorkerCount = %d, want %d", got, want)
	}
	if got, want := cfg.Server.StatsLogIntervalSecs, 60; got != want {
		t.Fatalf("StatsLogIntervalSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Metrics.Host, "127.0.0.1"; got != want {
		t.Fatalf("Metrics.Host = %q, want %q", got, want)
	}
	if got, want := cfg.Metrics.Port, 9163; got != want {
		t.Fatalf("Metrics.Port = %d, want %d", got, want)
	}
	if got, want := cfg.Metrics.Path, "/metrics"; got != want {
		t.Fatalf("Metrics.Path = %q, want %q", got, want)
	}
}

func TestLoadRejectsNonPositiveMaxDedupEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := strings.Join([]string{
		"server:",
		"  host: 127.0.0.1",
		"  port: 9161",
		"  max_dedup_entries: -1",
		"dedup_defaults:",
		"  key_fields:",
		"    - trap_oid",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadRejectsNonPositiveQueueSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := strings.Join([]string{
		"server:",
		"  host: 127.0.0.1",
		"  port: 9161",
		"  queue_size: -1",
		"dedup_defaults:",
		"  key_fields:",
		"    - trap_oid",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadRejectsMetricsPathWithoutLeadingSlash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := strings.Join([]string{
		"server:",
		"  host: 127.0.0.1",
		"  port: 9161",
		"metrics:",
		"  enabled: true",
		"  path: metrics",
		"dedup_defaults:",
		"  key_fields:",
		"    - trap_oid",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadAcceptsRuntimeMemoryLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := strings.Join([]string{
		"server:",
		"  host: 127.0.0.1",
		"  port: 9161",
		"runtime:",
		"  memory_limit: 128MiB",
		"dedup_defaults:",
		"  key_fields:",
		"    - trap_oid",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Runtime.MemoryLimit, "128MiB"; got != want {
		t.Fatalf("Runtime.MemoryLimit = %q, want %q", got, want)
	}
}

func TestLoadRejectsInvalidRuntimeMemoryLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := strings.Join([]string{
		"server:",
		"  host: 127.0.0.1",
		"  port: 9161",
		"runtime:",
		"  memory_limit: nope",
		"dedup_defaults:",
		"  key_fields:",
		"    - trap_oid",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}
