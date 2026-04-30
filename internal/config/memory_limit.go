package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var memoryLimitUnits = map[string]int64{
	"":    1,
	"B":   1,
	"KB":  1000,
	"MB":  1000 * 1000,
	"GB":  1000 * 1000 * 1000,
	"TB":  1000 * 1000 * 1000 * 1000,
	"KIB": 1024,
	"MIB": 1024 * 1024,
	"GIB": 1024 * 1024 * 1024,
	"TIB": 1024 * 1024 * 1024 * 1024,
}

func ParseMemoryLimit(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, nil
	}
	upper := strings.ToUpper(trimmed)
	if upper == "OFF" || upper == "NONE" || upper == "UNLIMITED" {
		return math.MaxInt64, nil
	}

	idx := 0
	for idx < len(trimmed) && trimmed[idx] >= '0' && trimmed[idx] <= '9' {
		idx++
	}
	if idx == 0 {
		return 0, fmt.Errorf("must start with a positive integer")
	}
	numberPart := trimmed[:idx]
	unitPart := strings.ToUpper(strings.TrimSpace(trimmed[idx:]))
	n, err := strconv.ParseInt(numberPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric value: %w", err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be greater than 0")
	}
	multiplier, ok := memoryLimitUnits[unitPart]
	if !ok {
		return 0, fmt.Errorf("unsupported unit %q", strings.TrimSpace(trimmed[idx:]))
	}
	if n > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("value overflows int64")
	}
	return n * multiplier, nil
}
