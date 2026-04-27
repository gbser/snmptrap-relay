package engine

import (
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sync/atomic"
	"time"

	"snmptrap-relay/internal/dedup"
	"snmptrap-relay/internal/match"
	"snmptrap-relay/internal/model"
	"snmptrap-relay/internal/oidutil"
)

type Decoder interface {
	Decode(data []byte, sourceIP string, sourcePort int) (*model.TrapEvent, error)
}

type PacketForwarder interface {
	Send([]byte) error
}

type runtimeState struct {
	cfg          *model.AppConfig
	forwarder    PacketForwarder
	decoder      Decoder
	logger       *slog.Logger
	alertsWriter io.Writer
}

type Engine struct {
	state atomic.Value
	dedup *dedup.Store
}

type Outcome struct {
	Accepted  bool
	Forwarded bool
	Reason    string
	RuleID    string
}

func New(cfg *model.AppConfig, forwarder PacketForwarder, decoder Decoder, logger *slog.Logger, alertsWriter io.Writer) *Engine {
	e := &Engine{
		dedup: dedup.NewStore(),
	}
	e.Reload(cfg, forwarder, decoder, logger, alertsWriter)
	return e
}

func (e *Engine) Reload(cfg *model.AppConfig, forwarder PacketForwarder, decoder Decoder, logger *slog.Logger, alertsWriter io.Writer) {
	e.state.Store(&runtimeState{
		cfg:          cfg,
		forwarder:    forwarder,
		decoder:      decoder,
		logger:       logger,
		alertsWriter: alertsWriter,
	})
}

func (e *Engine) Parse(data []byte, sourceIP string, sourcePort int) (*model.TrapEvent, error) {
	state := e.current()
	if state.decoder == nil {
		return nil, fmt.Errorf("decoder is not configured")
	}
	return state.decoder.Decode(data, sourceIP, sourcePort)
}

func (e *Engine) HandleEvent(event *model.TrapEvent) (Outcome, error) {
	state := e.current()
	if state == nil {
		return Outcome{}, fmt.Errorf("engine state not initialized")
	}

	if ruleID, matched, err := e.evaluateFilters(state.cfg, event); err != nil {
		return Outcome{}, err
	} else if matched {
		state.logger.Info("trap_filtered",
			"rule", ruleID,
			"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
			"trap_oid", fallback(event.TrapOID),
		)
		return Outcome{Accepted: false, Forwarded: false, Reason: "filtered", RuleID: ruleID}, nil
	}

	if e.applyAlarmClearRules(state, event) {
		return e.forwardPassThrough(state, event)
	}

	alarmRule, ok, err := e.matchAlarm(state.cfg, event)
	if err != nil {
		return Outcome{}, err
	}
	if !ok {
		return e.forwardPassThrough(state, event)
	}

	dedupKey, err := match.BuildDedupKey(event, alarmRule.Dedup.KeyFields)
	if err != nil {
		return Outcome{}, err
	}
	if len(dedupKey.MissingFields) > 0 || dedupKey.Hash == "" {
		if err := state.forwarder.Send(event.RawBytes); err != nil {
			state.logger.Error("trap_forward_failed",
				"rule", alarmRule.ID,
				"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
				"trap_oid", fallback(event.TrapOID),
				"missing_fields", dedupKey.MissingFields,
				"error", err,
			)
			return Outcome{Accepted: true, Forwarded: false, Reason: "forward_failed", RuleID: alarmRule.ID}, err
		}
		state.logger.Warn("trap_forwarded_dedup_disabled",
			"rule", alarmRule.ID,
			"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
			"trap_oid", fallback(event.TrapOID),
			"missing_fields", dedupKey.MissingFields,
		)
		if state.alertsWriter != nil && !event.AlertsLogged {
			// Build varbinds list similar to snmptrapd human-readable output
			var vbPairs []string
			for _, vb := range event.VarBinds {
				vbPairs = append(vbPairs, fmt.Sprintf("%s = \"%s\"", vb.OID, vb.Value))
			}
			vbs := ""
			if len(vbPairs) > 0 {
				vbs = "[ " + joinStrings(vbPairs, ", ") + " ]"
			}
			fmt.Fprintf(state.alertsWriter, "%s %s -> TRAP %s %s\n",
				time.Now().UTC().Format(time.RFC3339Nano),
				fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
				fallback(event.TrapOID),
				vbs,
			)
			event.AlertsLogged = true
		}
		return Outcome{Accepted: true, Forwarded: true, Reason: "dedup_disabled", RuleID: alarmRule.ID}, nil
	}

	if s := e.dedup.Get(alarmRule.ID, dedupKey.Hash, event.ReceivedAt); s != nil {
		s = e.dedup.Touch(alarmRule.ID, dedupKey.Hash, event)
		args := []any{
			"rule", alarmRule.ID,
			"key", dedupKey.Repr,
			"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
			"first_seen", s.FirstSeenAt.Format(time.RFC3339Nano),
			"first_source", fmt.Sprintf("%s:%d", s.FirstEvent.SourceIP, s.FirstEvent.SourcePort),
			"first_trap_oid", fallback(s.FirstEvent.TrapOID),
			"suppressed_count", s.SuppressedCount,
		}
		args = append(args, dedupLogFields(alarmRule.Dedup)...)
		state.logger.Info("trap_deduplicated", args...)
		return Outcome{Accepted: false, Forwarded: false, Reason: "duplicate", RuleID: alarmRule.ID}, nil
	}

	e.dedup.Put(alarmRule.ID, dedupKey.Hash, dedupKey.Repr, event, alarmRule.Dedup.TTLSeconds, alarmRule.Dedup.HoldUntilClear)
	if err := state.forwarder.Send(event.RawBytes); err != nil {
		state.logger.Error("trap_forward_failed",
			"rule", alarmRule.ID,
			"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
			"trap_oid", fallback(event.TrapOID),
			"error", err,
		)
		return Outcome{Accepted: true, Forwarded: false, Reason: "forward_failed", RuleID: alarmRule.ID}, err
	}
	args := []any{
		"rule", alarmRule.ID,
		"key", dedupKey.Repr,
		"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
		"trap_oid", fallback(event.TrapOID),
	}
	args = append(args, dedupLogFields(alarmRule.Dedup)...)
	state.logger.Info("trap_forwarded", args...)
	if state.alertsWriter != nil && !event.AlertsLogged {
		var vbPairs []string
		for _, vb := range event.VarBinds {
			vbPairs = append(vbPairs, fmt.Sprintf("%s = \"%s\"", vb.OID, vb.Value))
		}
		vbs := ""
		if len(vbPairs) > 0 {
			vbs = "[ " + joinStrings(vbPairs, ", ") + " ]"
		}
		fmt.Fprintf(state.alertsWriter, "%s %s -> TRAP %s %s\n",
			time.Now().UTC().Format(time.RFC3339Nano),
			fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
			fallback(event.TrapOID),
			vbs,
		)
		event.AlertsLogged = true
	}
	return Outcome{Accepted: true, Forwarded: true, Reason: "alarm", RuleID: alarmRule.ID}, nil
}

func (e *Engine) Cleanup(now time.Time) {
	expired := e.dedup.Cleanup(now)
	if len(expired) > 0 {
		state := e.current()
		if state != nil && state.logger != nil {
			state.logger.Debug("dedup_cleanup", "expired", len(expired))
		}
	}
}

func (e *Engine) current() *runtimeState {
	state, _ := e.state.Load().(*runtimeState)
	return state
}

// joinStrings is a minimal helper to join strings without importing strings
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += sep + parts[i]
	}
	return out
}

func (e *Engine) evaluateFilters(cfg *model.AppConfig, event *model.TrapEvent) (string, bool, error) {
	for _, rule := range cfg.Filters.Rules {
		matched, err := match.Matches(event, rule.Match)
		if err != nil {
			return "", false, err
		}
		if !matched {
			continue
		}
		switch rule.Action {
		case "", "drop":
			return rule.ID, true, nil
		case "keep":
			return rule.ID, false, nil
		default:
			return "", false, fmt.Errorf("unsupported filter action %q", rule.Action)
		}
	}
	if cfg.Filters.DefaultAction == "drop" {
		return "__default_drop__", true, nil
	}
	return "", false, nil
}

func (e *Engine) matchAlarm(cfg *model.AppConfig, event *model.TrapEvent) (model.AlarmRuleConfig, bool, error) {
	for _, rule := range cfg.Alarms {
		matched, err := match.Matches(event, rule.Match)
		if err != nil {
			return model.AlarmRuleConfig{}, false, err
		}
		if matched {
			return rule, true, nil
		}
	}
	return model.AlarmRuleConfig{}, false, nil
}

func (e *Engine) forwardPassThrough(state *runtimeState, event *model.TrapEvent) (Outcome, error) {
	if err := state.forwarder.Send(event.RawBytes); err != nil {
		state.logger.Error("trap_forward_failed",
			"rule", "pass-through",
			"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
			"trap_oid", fallback(event.TrapOID),
			"error", err,
		)
		return Outcome{Accepted: true, Forwarded: false, Reason: "forward_failed"}, err
	}
	state.logger.Info("trap_forwarded",
		"rule", "pass-through",
		"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
		"trap_oid", fallback(event.TrapOID),
	)

	if state.alertsWriter != nil && !event.AlertsLogged {
		var vbPairs []string
		for _, vb := range event.VarBinds {
			vbPairs = append(vbPairs, fmt.Sprintf("%s = \"%s\"", vb.OID, vb.Value))
		}
		vbs := ""
		if len(vbPairs) > 0 {
			vbs = "[ " + joinStrings(vbPairs, ", ") + " ]"
		}
		fmt.Fprintf(state.alertsWriter, "%s %s -> TRAP %s %s\n",
			time.Now().UTC().Format(time.RFC3339Nano),
			fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
			fallback(event.TrapOID),
			vbs,
		)
		event.AlertsLogged = true
	}
	return Outcome{Accepted: true, Forwarded: true, Reason: "pass-through"}, nil
}

func (e *Engine) applyAlarmClearRules(state *runtimeState, event *model.TrapEvent) bool {
	matchedClear := false
	for _, alarm := range state.cfg.Alarms {
		clearRule := alarm.Dedup.Clear
		if clearRule == nil {
			continue
		}
		matched, err := match.Matches(event, clearRule.Match)
		if err != nil {
			state.logger.Error("clear_match_error", "rule", alarm.ID, "error", err)
			continue
		}
		if !matched {
			continue
		}
		matchedClear = true
		// If the clear rule specifies a varbind OID and a regex, apply the
		// regex against the varbind value and only treat this trap as a clear
		// when the regex matches.
		if clearRule.VarBindOID != "" && clearRule.Regex != "" {
			// Normalize OID to match keys in RawVarBindMap
			oid := oidutil.Normalize(clearRule.VarBindOID)
			val := event.RawVarBindMap[oid]
			re, err := regexp.Compile(clearRule.Regex)
			if err != nil {
				state.logger.Error("clear_regex_invalid", "rule", alarm.ID, "regex", clearRule.Regex, "error", err)
				continue
			}
			if !re.MatchString(val) {
				// regex did not match: this trap is not considered a clear
				matchedClear = false
				continue
			}
		}
		keyFields := clearRule.KeyFields
		if len(keyFields) == 0 {
			keyFields = alarm.Dedup.KeyFields
		}
		clearKeyEvent := event
		if clearKeyTrapOID, ok := dedupKeyTrapOID(alarm.Match, keyFields); ok {
			clearKeyEvent = cloneEventWithTrapOID(event, clearKeyTrapOID)
		}
		dedupKey, err := match.BuildDedupKey(clearKeyEvent, keyFields)
		if err != nil {
			state.logger.Error("trap_clear_failed", "rule", alarm.ID, "error", err)
			continue
		}
		if len(dedupKey.MissingFields) > 0 || dedupKey.Hash == "" {
			state.logger.Warn("trap_clear_skipped",
				"rule", alarm.ID,
				"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
				"trap_oid", fallback(event.TrapOID),
				"missing_fields", dedupKey.MissingFields,
			)
			continue
		}
		stateCleared := e.dedup.Clear(alarm.ID, dedupKey.Hash)
		if stateCleared == nil {
			state.logger.Info("trap_cleared",
				"rule", alarm.ID,
				"key", dedupKey.Repr,
				"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
				"trap_oid", fallback(event.TrapOID),
				"active", false,
			)

			if state.alertsWriter != nil && !event.AlertsLogged {
				var vbPairs []string
				for _, vb := range event.VarBinds {
					vbPairs = append(vbPairs, fmt.Sprintf("%s = \"%s\"", vb.OID, vb.Value))
				}
				vbs := ""
				if len(vbPairs) > 0 {
					vbs = "[ " + joinStrings(vbPairs, ", ") + " ]"
				}
				fmt.Fprintf(state.alertsWriter, "%s %s -> TRAP %s %s\n",
					time.Now().UTC().Format(time.RFC3339Nano),
					fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
					fallback(event.TrapOID),
					vbs,
				)
				// mark event to avoid duplicate alerts writes later
				event.AlertsLogged = true
			}
			continue
		}
		state.logger.Info("trap_cleared",
			"rule", alarm.ID,
			"key", dedupKey.Repr,
			"source", fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
			"trap_oid", fallback(event.TrapOID),
			"active", true,
			"first_seen", stateCleared.FirstSeenAt.Format(time.RFC3339Nano),
			"first_source", fmt.Sprintf("%s:%d", stateCleared.FirstEvent.SourceIP, stateCleared.FirstEvent.SourcePort),
			"first_trap_oid", fallback(stateCleared.FirstEvent.TrapOID),
			"suppressed_count", stateCleared.SuppressedCount,
		)

		if state.alertsWriter != nil && !event.AlertsLogged {
			var vbPairs []string
			for _, vb := range event.VarBinds {
				vbPairs = append(vbPairs, fmt.Sprintf("%s = \"%s\"", vb.OID, vb.Value))
			}
			vbs := ""
			if len(vbPairs) > 0 {
				vbs = "[ " + joinStrings(vbPairs, ", ") + " ]"
			}
			fmt.Fprintf(state.alertsWriter, "%s %s -> TRAP %s %s\n",
				time.Now().UTC().Format(time.RFC3339Nano),
				fmt.Sprintf("%s:%d", event.SourceIP, event.SourcePort),
				fallback(event.TrapOID),
				vbs,
			)
			// mark event to avoid duplicate alerts writes later
			event.AlertsLogged = true
		}
	}
	return matchedClear
}

func fallback(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func dedupTTLDisplay(cfg model.DedupConfig) string {
	if cfg.HoldUntilClear {
		return "none"
	}
	return fmt.Sprintf("%ds", cfg.TTLSeconds)
}

func dedupLogFields(cfg model.DedupConfig) []any {
	fields := []any{"ttl", dedupTTLDisplay(cfg)}
	if cfg.HoldUntilClear {
		fields = append(fields,
			"hold_until_clear", true,
			"ttl_note", dedupTTLNote(cfg),
		)
		return fields
	}
	fields = append(fields, "ttl_seconds", cfg.TTLSeconds)
	return fields
}

func dedupTTLNote(cfg model.DedupConfig) string {
	if !cfg.HoldUntilClear {
		return ""
	}
	return "will not clear without a matching clear trap or a restart"
}

func dedupKeyTrapOID(spec model.MatchSpec, keyFields []string) (string, bool) {
	for _, field := range keyFields {
		if field == "trap_oid" {
			return staticMatchFieldValue(spec, "trap_oid")
		}
	}
	return "", false
}

func staticMatchFieldValue(spec model.MatchSpec, field string) (string, bool) {
	if value, ok := spec.Raw[field]; ok {
		return fmt.Sprint(value), true
	}
	rawAll, ok := spec.Raw["all"]
	if !ok {
		return "", false
	}
	list, ok := rawAll.([]any)
	if !ok {
		return "", false
	}
	resolved := ""
	found := false
	for _, item := range list {
		mapping, ok := item.(map[string]any)
		if !ok {
			converted, ok := item.(map[any]any)
			if !ok {
				continue
			}
			mapping = map[string]any{}
			for key, value := range converted {
				mapping[fmt.Sprint(key)] = value
			}
		}
		conditionField, _ := mapping["field"].(string)
		if conditionField != field {
			continue
		}
		op, _ := mapping["op"].(string)
		if op == "" {
			op = "eq"
		}
		if op != "eq" {
			continue
		}
		value := fmt.Sprint(mapping["value"])
		if found && resolved != value {
			return "", false
		}
		resolved = value
		found = true
	}
	return resolved, found
}

func cloneEventWithTrapOID(event *model.TrapEvent, trapOID string) *model.TrapEvent {
	if trapOID == "" {
		return event
	}
	clone := *event
	clone.TrapOID = trapOID
	if event.Fields == nil {
		clone.Fields = map[string]string{"trap_oid": trapOID}
		return &clone
	}
	clone.Fields = make(map[string]string, len(event.Fields)+1)
	for key, value := range event.Fields {
		clone.Fields[key] = value
	}
	clone.Fields["trap_oid"] = trapOID
	return &clone
}
