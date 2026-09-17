package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
)

// Accept optional metadata and cache hints for compatibility. Controls whose omission
// changes conversation behavior are validated explicitly after decoding.
func decodeRequest(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("request must contain a single JSON value")
	}
	return nil
}

func validEffort(effort string) bool {
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}
