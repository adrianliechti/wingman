package toolid

import "strings"

// Sanitize maps a cross-provider tool ID onto the ^[a-zA-Z0-9_-]{1,max}$
// pattern Anthropic (max 128) and Bedrock (max 64) require. Signature-packed
// "id::name::sig" composites are cut at the first "::"; remaining invalid
// runes become '_'.
func Sanitize(value string, max int) string {
	if index := strings.Index(value, "::"); index > 0 {
		value = value[:index]
	}

	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		}
		return '_'
	}, value)

	if len(value) > max {
		value = value[:max]
	}

	return value
}
