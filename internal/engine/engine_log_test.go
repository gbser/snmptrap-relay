package engine

import (
	"bytes"
	"log/slog"
	"testing"

	"snmptrap-relay/internal/model"
)

func TestHoldUntilClearLogsExplicitTTL(t *testing.T) {
	cfg := &model.AppConfig{
		Filters: model.FiltersConfig{DefaultAction: "keep"},
		Alarms: []model.AlarmRuleConfig{
			{
				ID: "demo",
				Match: model.MatchSpec{
					Raw: map[string]any{"trap_oid": "alarm.1"},
				},
				Dedup: model.DedupConfig{
					TTLSeconds:     60,
					HoldUntilClear: true,
					KeyFields:      []string{"trap_oid"},
					Clear: &model.DedupClearConfig{
						Match: model.MatchSpec{
							Raw: map[string]any{"trap_oid": "clear.1"},
						},
						KeyFields: []string{"trap_oid"},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	fwd := &fakeForwarder{}
	dec := &fakeDecoder{}
	eng := New(cfg, fwd, dec, logger, nil)

	ev := newEvent("10.0.0.1", 1000, "alarm.1", "", "")
	if _, err := eng.HandleEvent(ev); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	out := buf.String()
	if !bytes.Contains([]byte(out), []byte("ttl=none")) {
		t.Fatalf("log output missing ttl=none: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("hold_until_clear=true")) {
		t.Fatalf("log output missing hold_until_clear=true: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("will not clear without a matching clear trap or a restart")) {
		t.Fatalf("log output missing ttl note: %s", out)
	}
}
