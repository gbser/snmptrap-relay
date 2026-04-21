package match

import (
	"testing"

	"snmptrap-relay/internal/model"
)

func TestResolveFieldAcceptsDottedAndCanonicalVarbinds(t *testing.T) {
	event := &model.TrapEvent{
		Fields: map[string]string{
			"varbind.1.3.6.1.4.1.9999.1.1": "dev-01",
		},
	}

	if got, want := ResolveField(event, "varbind.1.3.6.1.4.1.9999.1.1"), "dev-01"; got != want {
		t.Fatalf("canonical varbind lookup = %q, want %q", got, want)
	}
	if got, want := ResolveField(event, "varbind:.1.3.6.1.4.1.9999.1.1"), "dev-01"; got != want {
		t.Fatalf("dotted varbind lookup = %q, want %q", got, want)
	}
}

func TestMatchesNormalizesOIDValues(t *testing.T) {
	event := &model.TrapEvent{
		Fields: map[string]string{
			"trap_oid": "1.3.6.1.4.1.9999.0.10",
		},
	}

	spec := model.MatchSpec{Raw: map[string]any{
		"trap_oid": ".1.3.6.1.4.1.9999.0.10",
	}}
	matched, err := Matches(event, spec)
	if err != nil {
		t.Fatalf("Matches() error = %v", err)
	}
	if !matched {
		t.Fatalf("Matches() = false, want true")
	}
}
