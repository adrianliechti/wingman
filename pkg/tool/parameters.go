package tool

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// StringParameter preserves the original text; callers decide whether to trim it.
func StringParameter(params map[string]any, key string) (string, error) {
	value := params[key]
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return text, nil
}

// StringsParameter accepts decoded JSON arrays and native Go string slices.
// Blank entries and duplicates are removed without reordering the values.
func StringsParameter(params map[string]any, key string) ([]string, error) {
	var values []string
	switch value := params[key].(type) {
	case nil:
		return nil, nil
	case []string:
		values = value
	case []any:
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must contain only strings", key)
			}
			values = append(values, text)
		}
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result, nil
}

// IntegerParameter rejects malformed numbers and caps valid values at maximum.
func IntegerParameter(params map[string]any, key string, fallback, minimum, maximum int) (int, error) {
	value := params[key]
	if value == nil {
		return min(max(fallback, minimum), maximum), nil
	}
	if n, ok := value.(int); ok {
		if n < minimum {
			return 0, fmt.Errorf("%s must be an integer of at least %d", key, minimum)
		}
		return min(n, maximum), nil
	}
	var n float64
	switch value := value.(type) {
	case float64:
		n = value
	case json.Number:
		var err error
		n, err = value.Float64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n < float64(minimum) || n >= float64(int(^uint(0)>>1)) {
		return 0, fmt.Errorf("%s must be an integer of at least %d", key, minimum)
	}
	return min(int(n), maximum), nil
}
