package openai

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
	"github.com/openai/openai-go/v3/responses"
)

// A server-side tool search result can omit its call ID.
type responseToolDecoder struct{ searchCallID string }

func (d *responseToolDecoder) decode(item any, responseID string, index int64) (provider.Content, error) {
	switch item := item.(type) {
	case responses.ResponseFunctionToolCall:
		return provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Name: item.Name, Namespace: item.Namespace, Async: item.Async, Arguments: item.Arguments}), nil
	case responses.ResponseCustomToolCall:
		return provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindCustom, Name: item.Name, Namespace: item.Namespace, Async: item.Async, Arguments: item.Input}), nil
	case responses.ResponseApplyPatchToolCall:
		args := (texteditor.Operation{Type: item.Operation.Type, Path: item.Operation.Path, Diff: item.Operation.Diff}).Args()
		return provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindTextEditor, Name: "apply_patch", Arguments: args}), nil

	case responses.ResponseComputerToolCall:
		args, _ := json.Marshal(computerCallToArgs(item), json.Deterministic(true))
		return provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindComputer, Name: "computer", Arguments: string(args)}), nil

	case responses.ResponseFunctionShellToolCall:
		return provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindShell, Name: shell.NameShell, Arguments: item.Action.RawJSON()}), nil

	case responses.ResponseOutputItemLocalShellCall:
		return provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindShell, Name: shell.NameLocalShell, Arguments: item.Action.RawJSON()}), nil

	case responses.ResponseToolSearchCall:
		d.searchCallID = item.CallID
		if d.searchCallID == "" {
			d.searchCallID = item.ID
			if d.searchCallID == "" {
				d.searchCallID = fmt.Sprintf("search_%s_%d", responseID, index)
			}
		}
		var raw struct {
			Arguments jsontext.Value `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(item.RawJSON()), &raw); err != nil {
			return provider.Content{}, err
		}
		args := raw.Arguments
		if len(args) == 0 {
			args = jsontext.Value("null")
		}
		return provider.ToolCallContent(provider.ToolCall{
			ID:        d.searchCallID,
			Kind:      provider.ToolKindToolSearch,
			Name:      "tool_search",
			Execution: string(item.Execution),
			Arguments: string(args),
		}), nil
	case responses.ResponseToolSearchOutputItem:
		id := item.CallID
		if id == "" {
			id = d.searchCallID
		}
		var raw struct {
			Tools jsontext.Value `json:"tools"`
		}
		if err := json.Unmarshal([]byte(item.RawJSON()), &raw); err != nil {
			return provider.Content{}, err
		}
		payload := raw.Tools
		if len(payload) == 0 || string(payload) == "null" {
			payload = jsontext.Value("[]")
		}
		return provider.ToolResultContent(provider.ToolResult{ID: id, Kind: provider.ToolKindToolSearch, Execution: string(item.Execution), Payload: payload}), nil
	}
	return provider.Content{}, nil
}

// Completed items retain their stream positions when a lifecycle snapshot
// refreshes them. A function call can acquire an item ID after its call ID.
type responseItemKey struct{ kind, id, callID string }

func itemKey(item responses.ResponseOutputItemUnion) responseItemKey {
	key := responseItemKey{item.Type, item.ID, item.CallID}
	switch item.Type {
	case "function_call", "custom_tool_call", "shell_call", "shell_call_output":
		key.id = ""
	}
	return key
}

type completedResponseOutput struct {
	open map[responseItemKey]bool

	items     map[int64]responses.ResponseOutputItemUnion
	positions map[responseItemKey]int64
	refreshed bool
}

func (o *completedResponseOutput) start(item responses.ResponseOutputItemUnion) {
	key := itemKey(item)
	if key.id == "" && key.callID == "" {
		return
	}
	if o.open == nil {
		o.open = make(map[responseItemKey]bool)
	}
	o.open[key] = true
}

func (o *completedResponseOutput) add(index int64, item responses.ResponseOutputItemUnion) error {
	if o.items == nil {
		o.items = make(map[int64]responses.ResponseOutputItemUnion)
		o.positions = make(map[responseItemKey]int64)
	}
	key := itemKey(item)
	delete(o.open, key)
	// Compatible providers can omit IDs on server-side tool results.
	if old, exists := o.items[index]; exists {
		delete(o.positions, itemKey(old))
	}
	if key.id != "" || key.callID != "" {
		if other, exists := o.positions[key]; exists && other != index {
			return fmt.Errorf("openai: duplicate output identity at positions %d and %d", other, index)
		}
		o.positions[key] = index
	}
	o.items[index] = item
	return nil
}

func (o *completedResponseOutput) position(item responses.ResponseOutputItemUnion) (int64, bool, error) {
	index, exists := o.positions[itemKey(item)]
	if !exists {
		return 0, false, nil
	}
	old := o.items[index]
	if old.ID != item.ID && !(old.ID == "" && (item.Type == "function_call" || item.Type == "custom_tool_call")) {
		return 0, false, fmt.Errorf("openai: item ID changed for %s", item.CallID)
	}
	return index, true, nil
}

func (o *completedResponseOutput) refresh(items []responses.ResponseOutputItemUnion) (provider.ReasoningSnapshots, error) {
	var snapshots provider.ReasoningSnapshots
	seen := make(map[responseItemKey]bool, len(items))
	for _, item := range items {
		key := itemKey(item)
		if seen[key] {
			return nil, fmt.Errorf("openai: duplicate item in lifecycle output: %v", key)
		}
		seen[key] = true
		index, known, err := o.position(item)
		if err != nil {
			return nil, err
		}
		if !known {
			continue
		}
		o.items[index] = item
		o.refreshed = true
		if item.Type == "reasoning" && item.EncryptedContent != "" {
			if snapshots == nil {
				snapshots = make(provider.ReasoningSnapshots)
			}
			snapshots[item.ID] = terminalReasoning(item.AsReasoning())
		}
	}
	return snapshots, nil
}

// Decode a full snapshot in its own order, independently of deltas which have
// already gone to clients. An empty output array is an authoritative empty turn.
func finalResponseContent(items []responses.ResponseOutputItemUnion, responseID string, status provider.CompletionStatus, signatures map[string]string, calls map[string]provider.ToolCall) ([]provider.Content, error) {
	if len(items) == 0 {
		return []provider.Content{}, nil
	}
	var decoder responseToolDecoder
	var acc provider.CompletionAccumulator
	var parts []provider.Content
	for i, output := range items {
		switch item := output.AsAny().(type) {
		case responses.ResponseOutputMessage:
			for _, p := range item.Content {
				c := provider.Content{MessageID: item.ID, Phase: provider.MessagePhase(item.Phase)}
				switch p.Type {
				case "output_text":
					c.Text, c.TextPresent = p.Text, true
				case "refusal":
					c.Refusal = p.Refusal
				default:
					continue
				}
				parts = append(parts, c)
			}
		case responses.ResponseReasoningItem:
			r := terminalReasoning(item)
			if r.Signature == "" {
				r.Signature = signatures[r.ID]
			}
			parts = append(parts, provider.ReasoningContent(r))
		case responses.ResponseCompactionItem:
			if item.EncryptedContent != "" {
				parts = append(parts, provider.CompactionContent(provider.Compaction{ID: item.ID, Signature: item.EncryptedContent}))
			}
		default:
			c, err := decoder.decode(item, responseID, int64(i))
			if err != nil {
				return nil, err
			}
			if c.ToolCall != nil {
				if !responseHasArguments(output) {
					c.ToolCall.Arguments = calls[c.ToolCall.ID].Arguments
				}
			} else if c.ToolResult == nil {
				continue
			}
			parts = append(parts, c)
		}
	}
	acc.Add(provider.Completion{Status: status, Message: &provider.Message{Content: parts}})
	content := acc.Result().Message.Content
	if content == nil {
		content = []provider.Content{}
	}
	return content, nil
}

func responseHasArguments(item responses.ResponseOutputItemUnion) bool {
	switch item.Type {
	case "function_call":
		return item.JSON.Arguments.Valid()
	case "custom_tool_call":
		return item.JSON.Input.Valid()
	default:
		return true
	}
}
