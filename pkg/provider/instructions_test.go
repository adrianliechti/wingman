package provider

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestInstructionLifetime(t *testing.T) {
	turn := Message{Role: MessageRoleSystem, Content: []Content{InstructionsContent(Instructions{Text: "temporary", Scope: InstructionScopeTurn})}}
	standing := Message{Role: MessageRoleSystem, Content: []Content{InstructionsContent(Instructions{Text: "standing"})}}
	for _, test := range []struct {
		name string
		next []Message
		live bool
	}{
		{"current turn", nil, true},
		{"assistant continuation", []Message{AssistantMessage("working")}, true},
		{"hosted search", []Message{{Role: MessageRoleUser, Content: []Content{ToolResultContent(ToolResult{Kind: ToolKindToolSearch, Execution: "server"})}}}, true},
		{"user turn", []Message{AssistantMessage("done"), UserMessage("next")}, false},
		{"client tool result", []Message{AssistantMessage("calling"), ToolMessage("call", "done")}, false},
		{"client search result", []Message{{Role: MessageRoleUser, Content: []Content{ToolResultContent(ToolResult{Kind: ToolKindToolSearch, Execution: "client"})}}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := append([]Message{standing, UserMessage("start"), turn}, test.next...)
			before, _ := json.Marshal(input)
			resolved := ResolveInstructions(input)
			want := []Message{SystemMessage("standing"), UserMessage("start")}
			if test.live {
				want = append(want, SystemMessage("temporary"))
			}
			want = append(want, test.next...)
			if !reflect.DeepEqual(resolved, want) {
				t.Fatalf("wrong lifetime: got %+v, want %+v", resolved, want)
			}
			after, _ := json.Marshal(input)
			if string(before) != string(after) {
				t.Fatal("changed history needed for native signed replay")
			}
		})
	}
}
