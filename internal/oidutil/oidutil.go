package oidutil

import "strings"

// Normalize trims whitespace and removes a single leading dot from an OID-like string.
func Normalize(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), ".")
}

// Lookup returns the value for an OID key, accepting dotted and non-dotted forms.
func Lookup(m map[string]string, key string) (string, bool) {
	if m == nil {
		return "", false
	}

	if value, ok := m[key]; ok {
		return value, true
	}

	normalized := Normalize(key)
	if normalized != key {
		if value, ok := m[normalized]; ok {
			return value, true
		}
	}

	dotted := "." + normalized
	if dotted != key && dotted != normalized {
		if value, ok := m[dotted]; ok {
			return value, true
		}
	}

	return "", false
}

// Variants returns the canonical OID form and the dotted form when different.
func Variants(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	normalized := Normalize(trimmed)
	if normalized == trimmed {
		return []string{normalized}
	}
	return []string{normalized, trimmed}
}
