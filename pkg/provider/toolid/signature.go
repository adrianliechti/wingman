package toolid

import (
	"encoding/base64"
	"strings"
)

// StripSignature removes a valid signature from a composite tool ID while
// preserving the call ID and tool name needed to replay the result.
func StripSignature(value string) string {
	parts := strings.Split(value, "::")
	if len(parts) < 3 {
		return value
	}

	if _, err := base64.StdEncoding.DecodeString(parts[2]); err != nil {
		return value
	}

	return parts[0] + "::" + parts[1]
}
