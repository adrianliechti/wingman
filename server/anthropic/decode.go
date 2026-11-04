package anthropic

import (
	"io"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
)

// Accept optional metadata and cache hints for compatibility. Controls whose omission
// changes conversation behavior are validated explicitly after decoding.
func decodeRequest(r io.Reader, target any) error {
	return jsonutil.UnmarshalRead(r, target)
}

func validEffort(effort string) bool {
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}
