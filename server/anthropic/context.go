package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func (e ContextManagementEdit) reasoningContext() (provider.ReasoningContext, error) {
	keep := bytes.TrimSpace(e.Keep)
	if len(keep) == 0 || bytes.Equal(keep, []byte("null")) {
		return provider.ReasoningContextAuto, nil
	}
	if bytes.Equal(keep, []byte(`"all"`)) {
		return provider.ReasoningContextAllTurns, nil
	}
	var retention struct {
		Type  string `json:"type"`
		Value *int   `json:"value"`
	}
	if err := json.Unmarshal(keep, &retention); err == nil {
		if retention.Type == "all" && retention.Value == nil {
			return provider.ReasoningContextAllTurns, nil
		}
		if retention.Type == "thinking_turns" && retention.Value != nil && *retention.Value == 1 {
			return provider.ReasoningContextCurrentTurn, nil
		}
	}
	return "", fmt.Errorf("context_management: thinking keep must be all or one thinking turn")
}
