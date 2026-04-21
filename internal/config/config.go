package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"snmptrap-relay/internal/model"
)

func Load(path string) (*model.AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg model.AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(cfg *model.AppConfig) {
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 162
	}
	if cfg.Server.MaxDatagramSize == 0 {
		cfg.Server.MaxDatagramSize = 8192
	}
	if cfg.Server.CleanupIntervalSeconds == 0 {
		cfg.Server.CleanupIntervalSeconds = 30
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "INFO"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "text"
	}
	for i := range cfg.Receiver.V3Users {
		cfg.Receiver.V3Users[i].UserName = strings.TrimSpace(cfg.Receiver.V3Users[i].UserName)
		cfg.Receiver.V3Users[i].AuthenticationProtocol = strings.ToLower(strings.TrimSpace(cfg.Receiver.V3Users[i].AuthenticationProtocol))
		cfg.Receiver.V3Users[i].PrivacyProtocol = strings.ToLower(strings.TrimSpace(cfg.Receiver.V3Users[i].PrivacyProtocol))
	}
	if cfg.FieldAliases == nil {
		cfg.FieldAliases = map[string]string{}
	}
	if cfg.Filters.DefaultAction == "" {
		cfg.Filters.DefaultAction = "keep"
	}
	cfg.Filters.DefaultAction = strings.ToLower(cfg.Filters.DefaultAction)
	if cfg.DedupDefaults.TTLSeconds == 0 {
		cfg.DedupDefaults.TTLSeconds = 300
	}
}

func validate(cfg *model.AppConfig) error {
	if cfg.Filters.DefaultAction != "keep" && cfg.Filters.DefaultAction != "drop" {
		return fmt.Errorf("filters.default_action must be keep or drop")
	}

	seen := map[string]string{}
	for _, user := range cfg.Receiver.V3Users {
		if user.UserName == "" {
			return fmt.Errorf("receiver.v3_users[].user_name is required")
		}
	}
	for _, rule := range cfg.Filters.Rules {
		if rule.ID == "" {
			return fmt.Errorf("filters rule id is required")
		}
		if _, ok := seen["filter:"+rule.ID]; ok {
			return fmt.Errorf("duplicate rule id %q", rule.ID)
		}
		seen["filter:"+rule.ID] = rule.ID
		action := strings.ToLower(rule.Action)
		if action == "" {
			action = "drop"
		}
		if action != "drop" && action != "keep" {
			return fmt.Errorf("filter rule %q has unsupported action %q", rule.ID, rule.Action)
		}
	}
	for i := range cfg.Filters.Rules {
		action := strings.ToLower(cfg.Filters.Rules[i].Action)
		if action == "" {
			action = "drop"
		}
		cfg.Filters.Rules[i].Action = action
	}
	for i := range cfg.Alarms {
		rule := &cfg.Alarms[i]
		if rule.ID == "" {
			return fmt.Errorf("alarm rule id is required")
		}
		if _, ok := seen["alarm:"+rule.ID]; ok {
			return fmt.Errorf("duplicate rule id %q", rule.ID)
		}
		seen["alarm:"+rule.ID] = rule.ID
		if rule.Dedup.TTLSeconds == 0 {
			rule.Dedup.TTLSeconds = cfg.DedupDefaults.TTLSeconds
		}
		if len(rule.Dedup.KeyFields) == 0 {
			rule.Dedup.KeyFields = append([]string{}, cfg.DedupDefaults.KeyFields...)
		}
		if len(rule.Dedup.KeyFields) == 0 {
			return fmt.Errorf("alarm %q requires dedup.key_fields", rule.ID)
		}
	}

	for _, alarm := range cfg.Alarms {
		if alarm.Dedup.Clear != nil && len(alarm.Dedup.Clear.Match.Raw) == 0 {
			return fmt.Errorf("alarm %q dedup.clear.match is required", alarm.ID)
		}
		if alarm.Dedup.HoldUntilClear && alarm.Dedup.Clear == nil {
			return fmt.Errorf("alarm %q requires dedup.clear when dedup.hold_until_clear is true", alarm.ID)
		}
	}
	return nil
}
