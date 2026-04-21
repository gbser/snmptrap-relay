package match

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"snmptrap-relay/internal/model"
	"snmptrap-relay/internal/oidutil"
)

type keyItem struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type condition struct {
	Field string
	Op    string
	Value any
}

func ResolveField(event *model.TrapEvent, field string) string {
	if field == "" {
		return ""
	}
	if value, ok := event.Fields[field]; ok {
		return value
	}
	if strings.HasPrefix(field, "fields.") {
		name := strings.TrimPrefix(field, "fields.")
		if value, ok := event.Fields[name]; ok {
			return value
		}
	}
	if strings.HasPrefix(field, "varbind.") {
		oid := strings.TrimPrefix(field, "varbind.")
		for _, variant := range oidutil.Variants(oid) {
			if value, ok := event.Fields["varbind."+variant]; ok {
				return value
			}
		}
	}
	if strings.HasPrefix(field, "varbind:") {
		oid := strings.TrimPrefix(field, "varbind:")
		for _, variant := range oidutil.Variants(oid) {
			if value, ok := event.Fields["varbind."+variant]; ok {
				return value
			}
		}
	}
	return ""
}

func Matches(event *model.TrapEvent, spec model.MatchSpec) (bool, error) {
	if len(spec.Raw) == 0 {
		return true, nil
	}

	if rawList, ok := spec.Raw["all"]; ok || spec.Raw["any"] != nil || spec.Raw["not"] != nil {
		var allConds []condition
		var anyConds []condition
		var notConds []condition

		if ok {
			conds, err := parseConditionList(rawList)
			if err != nil {
				return false, err
			}
			allConds = conds
		}
		if spec.Raw["any"] != nil {
			conds, err := parseConditionList(spec.Raw["any"])
			if err != nil {
				return false, err
			}
			anyConds = conds
		}
		if spec.Raw["not"] != nil {
			conds, err := parseConditionList(spec.Raw["not"])
			if err != nil {
				return false, err
			}
			notConds = conds
		}

		for _, cond := range allConds {
			ok, err := conditionMatches(event, cond)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		if len(anyConds) > 0 {
			matched := false
			for _, cond := range anyConds {
				ok, err := conditionMatches(event, cond)
				if err != nil {
					return false, err
				}
				if ok {
					matched = true
					break
				}
			}
			if !matched {
				return false, nil
			}
		}
		for _, cond := range notConds {
			ok, err := conditionMatches(event, cond)
			if err != nil {
				return false, err
			}
			if ok {
				return false, nil
			}
		}
		return true, nil
	}

	for field, expected := range spec.Raw {
		actual := ResolveField(event, field)
		expectedValue := fmt.Sprint(expected)
		if isOIDField(field) {
			actual = oidutil.Normalize(actual)
			expectedValue = oidutil.Normalize(expectedValue)
		}
		if expectedValue != actual {
			return false, nil
		}
	}
	return true, nil
}

func parseConditionList(value any) ([]condition, error) {
	switch typed := value.(type) {
	case []interface{}:
		conds := make([]condition, 0, len(typed))
		for _, item := range typed {
			m, ok := item.(map[string]any)
			if !ok {
				if converted, ok := item.(map[any]any); ok {
					temp := map[string]any{}
					for k, v := range converted {
						temp[fmt.Sprint(k)] = v
					}
					m = temp
				} else {
					return nil, fmt.Errorf("match condition must be a mapping")
				}
			}
			field, _ := m["field"].(string)
			op, _ := m["op"].(string)
			if op == "" {
				op = "eq"
			}
			conds = append(conds, condition{Field: field, Op: op, Value: m["value"]})
		}
		return conds, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("match list must be a slice")
	}
}

func conditionMatches(event *model.TrapEvent, cond condition) (bool, error) {
	actual := ResolveField(event, cond.Field)
	if isOIDField(cond.Field) {
		actual = oidutil.Normalize(actual)
	}
	op := strings.ToLower(cond.Op)

	switch op {
	case "exists":
		return actual != "", nil
	case "eq":
		expected := fmt.Sprint(cond.Value)
		if isOIDField(cond.Field) {
			expected = oidutil.Normalize(expected)
		}
		return actual == expected, nil
	case "ne":
		expected := fmt.Sprint(cond.Value)
		if isOIDField(cond.Field) {
			expected = oidutil.Normalize(expected)
		}
		return actual != expected, nil
	case "contains":
		expected := fmt.Sprint(cond.Value)
		if isOIDField(cond.Field) {
			expected = oidutil.Normalize(expected)
		}
		return strings.Contains(actual, expected), nil
	case "prefix":
		expected := fmt.Sprint(cond.Value)
		if isOIDField(cond.Field) {
			expected = oidutil.Normalize(expected)
		}
		return strings.HasPrefix(actual, expected), nil
	case "suffix":
		expected := fmt.Sprint(cond.Value)
		if isOIDField(cond.Field) {
			expected = oidutil.Normalize(expected)
		}
		return strings.HasSuffix(actual, expected), nil
	case "regex":
		expected := fmt.Sprint(cond.Value)
		if isOIDField(cond.Field) {
			expected = oidutil.Normalize(expected)
		}
		return regexp.MatchString(expected, actual)
	case "in":
		return inList(actual, cond.Value, isOIDField(cond.Field)), nil
	case "gt", "ge", "lt", "le":
		left, err := parseNumber(actual)
		if err != nil {
			return false, nil
		}
		right, err := parseNumber(fmt.Sprint(cond.Value))
		if err != nil {
			return false, nil
		}
		switch op {
		case "gt":
			return left > right, nil
		case "ge":
			return left >= right, nil
		case "lt":
			return left < right, nil
		case "le":
			return left <= right, nil
		}
	}
	return false, fmt.Errorf("unsupported operator %q", cond.Op)
}

func inList(actual string, value any, normalize bool) bool {
	switch typed := value.(type) {
	case []interface{}:
		for _, item := range typed {
			expected := fmt.Sprint(item)
			if normalize {
				expected = oidutil.Normalize(expected)
			}
			if actual == expected {
				return true
			}
		}
		return false
	default:
		rv := reflect.ValueOf(value)
		if rv.IsValid() && rv.Kind() == reflect.Slice {
			for i := 0; i < rv.Len(); i++ {
				expected := fmt.Sprint(rv.Index(i).Interface())
				if normalize {
					expected = oidutil.Normalize(expected)
				}
				if actual == expected {
					return true
				}
			}
		}
		return false
	}
}

func isOIDField(field string) bool {
	return field == "trap_oid" ||
		field == "enterprise_oid" ||
		strings.HasPrefix(field, "varbind.") ||
		strings.HasPrefix(field, "varbind:")
}

func parseNumber(value string) (float64, error) {
	if strings.Contains(value, ".") {
		return strconv.ParseFloat(value, 64)
	}
	return strconv.ParseFloat(value, 64)
}

func BuildDedupKey(event *model.TrapEvent, keyFields []string) (model.DedupKey, error) {
	items := make([]keyItem, 0, len(keyFields))
	missing := make([]string, 0)
	for _, field := range keyFields {
		value := ResolveField(event, field)
		if value == "" {
			missing = append(missing, field)
			continue
		}
		items = append(items, keyItem{Field: field, Value: value})
	}
	if len(missing) > 0 || len(items) == 0 {
		return model.DedupKey{MissingFields: missing}, nil
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return model.DedupKey{}, err
	}
	sum := sha256.Sum256(payload)
	return model.DedupKey{
		Hash: fmt.Sprintf("%x", sum[:]),
		Repr: string(payload),
	}, nil
}
