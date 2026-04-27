package model

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Host                   string `yaml:"host"`
	Port                   int    `yaml:"port"`
	MaxDatagramSize        int    `yaml:"max_datagram_size"`
	CleanupIntervalSeconds int    `yaml:"cleanup_interval_seconds"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	File   string `yaml:"file"`
	// AlertsFile is an optional separate file path where only forwarded
	// alerts are written. Useful for log-parsing exporters that should
	// consume only deduplicated alerts.
	AlertsFile string `yaml:"alerts_file"`
}

type ForwarderConfig struct {
	Name    string `yaml:"name"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	Enabled bool   `yaml:"enabled"`
}

type SnmpV3UserConfig struct {
	UserName                 string `yaml:"user_name"`
	AuthenticationProtocol   string `yaml:"authentication_protocol"`
	AuthenticationPassphrase string `yaml:"authentication_passphrase"`
	PrivacyProtocol          string `yaml:"privacy_protocol"`
	PrivacyPassphrase        string `yaml:"privacy_passphrase"`
}

type ReceiverConfig struct {
	V3Users []SnmpV3UserConfig `yaml:"v3_users"`
}

type MatchSpec struct {
	Raw map[string]any
}

func (m *MatchSpec) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Kind == 0 {
		m.Raw = nil
		return nil
	}
	var raw map[string]any
	if err := value.Decode(&raw); err != nil {
		return err
	}
	m.Raw = raw
	return nil
}

type DedupConfig struct {
	TTLSeconds     int               `yaml:"ttl_seconds"`
	KeyFields      []string          `yaml:"key_fields"`
	HoldUntilClear bool              `yaml:"hold_until_clear"`
	Clear          *DedupClearConfig `yaml:"clear"`
}

type DedupClearConfig struct {
	Match     MatchSpec `yaml:"match"`
	KeyFields []string  `yaml:"key_fields"`
	// VarBindOID is the OID of the varbind to test with Regex when deciding
	// whether a trap is a clear for a held alarm. Example: "1.3.6.1.4.1..."
	VarBindOID string `yaml:"varbind_oid"`
	// Regex is applied to the varbind value (string) and if it matches the
	// trap is considered a clear. The regex should be a Go-compatible regexp.
	Regex string `yaml:"regex"`
}

type FilterRuleConfig struct {
	ID     string    `yaml:"id"`
	Action string    `yaml:"action"`
	Match  MatchSpec `yaml:"match"`
}

type FiltersConfig struct {
	DefaultAction string             `yaml:"default_action"`
	Rules         []FilterRuleConfig `yaml:"rules"`
}

type AlarmRuleConfig struct {
	ID    string      `yaml:"id"`
	Match MatchSpec   `yaml:"match"`
	Dedup DedupConfig `yaml:"dedup"`
}

type AppConfig struct {
	Server        ServerConfig      `yaml:"server"`
	Logging       LoggingConfig     `yaml:"logging"`
	Receiver      ReceiverConfig    `yaml:"receiver"`
	FieldAliases  map[string]string `yaml:"field_aliases"`
	Forwarders    []ForwarderConfig `yaml:"forwarders"`
	Filters       FiltersConfig     `yaml:"filters"`
	DedupDefaults DedupConfig       `yaml:"dedup_defaults"`
	Alarms        []AlarmRuleConfig `yaml:"alarms"`
}

type VarBind struct {
	OID      string
	TypeName string
	Value    string
}

type TrapEvent struct {
	ReceivedAt      time.Time
	SourceIP        string
	SourcePort      int
	RawBytes        []byte
	Version         int
	Community       string
	PDUType         string
	EnterpriseOID   string
	AgentAddress    string
	GenericTrap     string
	GenericTrapName string
	SpecificTrap    string
	Uptime          string
	RequestID       int64
	ErrorStatus     int64
	ErrorIndex      int64
	TrapOID         string
	VarBinds        []VarBind
	RawVarBindMap   map[string]string
	Fields          map[string]string
	// AlertsLogged indicates whether this event has already been written
	// to the alerts file to avoid duplicate entries.
	AlertsLogged bool
}

func (e *TrapEvent) Summary() string {
	parts := []string{
		fmt.Sprintf("src=%s:%d", e.SourceIP, e.SourcePort),
		fmt.Sprintf("version=%d", e.Version),
	}
	if e.TrapOID != "" {
		parts = append(parts, fmt.Sprintf("trap_oid=%s", e.TrapOID))
	} else if e.PDUType != "" {
		parts = append(parts, fmt.Sprintf("pdu=%s", e.PDUType))
	}
	if e.EnterpriseOID != "" {
		parts = append(parts, fmt.Sprintf("enterprise=%s", e.EnterpriseOID))
	}
	if e.GenericTrapName != "" {
		parts = append(parts, fmt.Sprintf("generic=%s", e.GenericTrapName))
	} else if e.GenericTrap != "" {
		parts = append(parts, fmt.Sprintf("generic=%s", e.GenericTrap))
	}
	if e.SpecificTrap != "" {
		parts = append(parts, fmt.Sprintf("specific=%s", e.SpecificTrap))
	}
	return fmt.Sprintf("%s", joinParts(parts))
}

func joinParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += " " + parts[i]
	}
	return out
}

type DedupKey struct {
	Hash          string
	Repr          string
	MissingFields []string
}

type DedupState struct {
	RuleID          string
	KeyHash         string
	KeyRepr         string
	FirstEvent      *TrapEvent
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	TTLSeconds      int
	HoldUntilClear  bool
	SuppressedCount int
}

func (s *DedupState) ExpiresAt() time.Time {
	return s.FirstSeenAt.Add(time.Duration(s.TTLSeconds) * time.Second)
}
