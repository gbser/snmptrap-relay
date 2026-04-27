package engine

import (
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"snmptrap-relay/internal/model"
)

type fakeForwarder struct {
	sent [][]byte
}

type fakeDecoder struct{}

func (f *fakeDecoder) Decode(data []byte, sourceIP string, sourcePort int) (*model.TrapEvent, error) {
	return nil, nil
}

func (f *fakeForwarder) Send(payload []byte) error {
	cp := append([]byte(nil), payload...)
	f.sent = append(f.sent, cp)
	return nil
}

func TestDedupAndClear(t *testing.T) {
	cfg := &model.AppConfig{
		Filters: model.FiltersConfig{DefaultAction: "keep"},
		Alarms: []model.AlarmRuleConfig{
			{
				ID: "link_down",
				Match: model.MatchSpec{
					Raw: map[string]any{"trap_oid": "alarm.1"},
				},
				Dedup: model.DedupConfig{
					TTLSeconds: 60,
					KeyFields:  []string{"fields.ifIndex", "fields.device_id"},
					Clear: &model.DedupClearConfig{
						Match: model.MatchSpec{
							Raw: map[string]any{"trap_oid": "clear.1"},
						},
						KeyFields: []string{"fields.ifIndex", "fields.device_id"},
					},
				},
			},
		},
	}

	fwd := &fakeForwarder{}
	dec := &fakeDecoder{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := New(cfg, fwd, dec, logger, nil)

	first := newEvent("10.0.0.1", 1000, "alarm.1", "7", "A")
	if _, err := eng.HandleEvent(first); err != nil {
		t.Fatalf("HandleEvent(first) error = %v", err)
	}
	if got, want := len(fwd.sent), 1; got != want {
		t.Fatalf("forward count after first = %d, want %d", got, want)
	}

	dup := newEvent("10.0.0.2", 1001, "alarm.1", "7", "A")
	if _, err := eng.HandleEvent(dup); err != nil {
		t.Fatalf("HandleEvent(dup) error = %v", err)
	}
	if got, want := len(fwd.sent), 1; got != want {
		t.Fatalf("forward count after duplicate = %d, want %d", got, want)
	}

	clear := newEvent("10.0.0.9", 1002, "clear.1", "7", "A")
	if _, err := eng.HandleEvent(clear); err != nil {
		t.Fatalf("HandleEvent(clear) error = %v", err)
	}
	if got, want := len(fwd.sent), 2; got != want {
		t.Fatalf("forward count after clear = %d, want %d", got, want)
	}

	afterClear := newEvent("10.0.0.3", 1003, "alarm.1", "7", "A")
	if _, err := eng.HandleEvent(afterClear); err != nil {
		t.Fatalf("HandleEvent(afterClear) error = %v", err)
	}
	if got, want := len(fwd.sent), 3; got != want {
		t.Fatalf("forward count after clear reset = %d, want %d", got, want)
	}
}

func TestDedupAndClearWithDifferentClearTrapOIDAndTrapOIDInKey(t *testing.T) {
	cfg := &model.AppConfig{
		Filters: model.FiltersConfig{DefaultAction: "keep"},
		Alarms: []model.AlarmRuleConfig{
			{
				ID: "link_down",
				Match: model.MatchSpec{
					Raw: map[string]any{"trap_oid": "alarm.1"},
				},
				Dedup: model.DedupConfig{
					TTLSeconds:     60,
					KeyFields:      []string{"trap_oid", "fields.ifIndex", "fields.device_id"},
					HoldUntilClear: true,
					Clear: &model.DedupClearConfig{
						Match: model.MatchSpec{
							Raw: map[string]any{"trap_oid": "clear.1"},
						},
						KeyFields: []string{"trap_oid", "fields.ifIndex", "fields.device_id"},
					},
				},
			},
		},
	}

	fwd := &fakeForwarder{}
	dec := &fakeDecoder{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := New(cfg, fwd, dec, logger, nil)

	first := newEvent("10.0.0.1", 1000, "alarm.1", "7", "A")
	if _, err := eng.HandleEvent(first); err != nil {
		t.Fatalf("HandleEvent(first) error = %v", err)
	}

	dup := newEvent("10.0.0.2", 1001, "alarm.1", "7", "A")
	if _, err := eng.HandleEvent(dup); err != nil {
		t.Fatalf("HandleEvent(dup) error = %v", err)
	}
	if got, want := len(fwd.sent), 1; got != want {
		t.Fatalf("forward count after duplicate = %d, want %d", got, want)
	}

	clear := newEvent("10.0.0.9", 1002, "clear.1", "7", "A")
	if _, err := eng.HandleEvent(clear); err != nil {
		t.Fatalf("HandleEvent(clear) error = %v", err)
	}
	if got, want := len(fwd.sent), 2; got != want {
		t.Fatalf("forward count after clear = %d, want %d", got, want)
	}

	afterClear := newEvent("10.0.0.3", 1003, "alarm.1", "7", "A")
	if _, err := eng.HandleEvent(afterClear); err != nil {
		t.Fatalf("HandleEvent(afterClear) error = %v", err)
	}
	if got, want := len(fwd.sent), 3; got != want {
		t.Fatalf("forward count after clear reset = %d, want %d", got, want)
	}
}

func TestDedupAndClearWithSameTrapOIDAndRegexStyleMatch(t *testing.T) {
	cfg := &model.AppConfig{
		Filters: model.FiltersConfig{DefaultAction: "keep"},
		Alarms: []model.AlarmRuleConfig{
			{
				ID: "link_down",
				Match: model.MatchSpec{
					Raw: map[string]any{"trap_oid": "alarm.1"},
				},
				Dedup: model.DedupConfig{
					TTLSeconds:     60,
					KeyFields:      []string{"trap_oid", "fields.ifIndex", "fields.device_id"},
					HoldUntilClear: true,
					Clear: &model.DedupClearConfig{
						Match: model.MatchSpec{
							Raw: map[string]any{"trap_oid": "alarm.1"},
						},
						KeyFields:  []string{"trap_oid", "fields.ifIndex", "fields.device_id"},
						VarBindOID: "1.3.6.1.4.1.9999.1.3",
						Regex:      "(?i)clear",
					},
				},
			},
		},
	}

	fwd := &fakeForwarder{}
	dec := &fakeDecoder{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := New(cfg, fwd, dec, logger, nil)

	first := newEvent("10.0.0.1", 1000, "alarm.1", "7", "A")
	if _, err := eng.HandleEvent(first); err != nil {
		t.Fatalf("HandleEvent(first) error = %v", err)
	}

	clear := newEvent("10.0.0.9", 1002, "alarm.1", "7", "A")
	clear.RawVarBindMap["1.3.6.1.4.1.9999.1.3"] = "clear"
	if _, err := eng.HandleEvent(clear); err != nil {
		t.Fatalf("HandleEvent(clear) error = %v", err)
	}

	afterClear := newEvent("10.0.0.3", 1003, "alarm.1", "7", "A")
	if _, err := eng.HandleEvent(afterClear); err != nil {
		t.Fatalf("HandleEvent(afterClear) error = %v", err)
	}
	if got, want := len(fwd.sent), 3; got != want {
		t.Fatalf("forward count after clear reset = %d, want %d", got, want)
	}
}

func TestFilterDrop(t *testing.T) {
	cfg := &model.AppConfig{
		Filters: model.FiltersConfig{
			DefaultAction: "keep",
			Rules: []model.FilterRuleConfig{
				{
					ID:     "drop_test",
					Action: "drop",
					Match:  model.MatchSpec{Raw: map[string]any{"trap_oid": "drop.1"}},
				},
			},
		},
	}

	fwd := &fakeForwarder{}
	dec := &fakeDecoder{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := New(cfg, fwd, dec, logger, nil)

	ev := newEvent("10.0.0.1", 1000, "drop.1", "7", "A")
	if _, err := eng.HandleEvent(ev); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	if got := len(fwd.sent); got != 0 {
		t.Fatalf("forward count after filter drop = %d, want 0", got)
	}
}

func newEvent(sourceIP string, sourcePort int, trapOID, ifIndex, deviceID string) *model.TrapEvent {
	fields := map[string]string{
		"source_ip":        sourceIP,
		"source_port":      strconv.Itoa(sourcePort),
		"trap_oid":         trapOID,
		"fields.ifIndex":   ifIndex,
		"fields.device_id": deviceID,
		"ifIndex":          ifIndex,
		"device_id":        deviceID,
	}
	return &model.TrapEvent{
		SourceIP:      sourceIP,
		SourcePort:    sourcePort,
		ReceivedAt:    time.Now().UTC(),
		RawBytes:      []byte("packet"),
		TrapOID:       trapOID,
		Fields:        fields,
		RawVarBindMap: map[string]string{},
	}
}
