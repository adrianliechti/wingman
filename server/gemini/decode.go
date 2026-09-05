package gemini

import (
	"encoding/json"
	"io"
	"strings"
)

// The Gemini REST API speaks protobuf JSON: field names may be written in
// snake_case or lowerCamelCase, and a repeated field may carry a single
// object instead of an array. Google's own shell examples use both forms.
// decodeRequest normalizes a body to the lowerCamel, array form the request
// structs expect before decoding it.
func decodeRequest(r io.Reader, v any) error {
	var raw any

	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return err
	}

	data, err := json.Marshal(normalizeRequest(raw))
	if err != nil {
		return err
	}

	return json.Unmarshal(data, v)
}

// repeatedFields are wrapped into an array when given as a single value.
var repeatedFields = map[string]bool{
	"contents":             true,
	"parts":                true,
	"tools":                true,
	"functionDeclarations": true,
	"safetySettings":       true,
	"stopSequences":        true,
	"responseModalities":   true,
	"allowedFunctionNames": true,
}

// opaqueFields hold caller data (function arguments, schemas) whose keys
// must not be rewritten.
var opaqueFields = map[string]bool{
	"args":                 true,
	"response":             true,
	"parameters":           true,
	"parametersJsonSchema": true,
	"responseSchema":       true,
	"responseJsonSchema":   true,
}

func normalizeRequest(v any) any {
	switch value := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))

		for key, item := range value {
			key = camelCase(key)

			if opaqueFields[key] {
				result[key] = item
				continue
			}

			if key == "systemInstruction" {
				if text, ok := item.(string); ok {
					item = map[string]any{"parts": []any{map[string]any{"text": text}}}
				}
			}

			if repeatedFields[key] {
				if _, isList := item.([]any); !isList {
					item = []any{item}
				}
			}

			result[key] = normalizeRequest(item)
		}

		return result

	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = normalizeRequest(item)
		}
		return result

	default:
		return v
	}
}

// camelCase converts a snake_case protobuf field name to its JSON name.
// Names without underscores come back unchanged.
func camelCase(name string) string {
	if !strings.Contains(name, "_") {
		return name
	}

	var b strings.Builder
	upper := false

	for i, r := range name {
		if r == '_' && i > 0 {
			upper = true
			continue
		}

		if upper {
			b.WriteString(strings.ToUpper(string(r)))
			upper = false
			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}
