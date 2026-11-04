package gemini

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const preciseArguments = `{"id":9007199254740993,"values":[1.234567890123456789,1e400,null,false]}`

func TestArgumentlessFunctionCallReplay(t *testing.T) {
	for _, args := range []string{"", `,"args":null`, `,"args":{}`} {
		var req GenerateContentRequest
		body := `{"contents":[{"role":"model","parts":[{"functionCall":{"name":"get_time"` + args + `}}]}]}`
		if err := decodeRequest(strings.NewReader(body), &req); err != nil {
			t.Fatal(err)
		}
		messages, err := toMessages(nil, req.Contents)
		if err != nil {
			t.Fatal(err)
		}
		if got := messages[0].ToolCalls()[0].Arguments; got != "{}" {
			t.Fatalf("argumentless function call replays as %q, want {}", got)
		}
	}
}

func requirePreciseArguments(t *testing.T, value any) {
	t.Helper()
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]jsontext.Value
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["id"]) != "9007199254740993" || string(got["values"]) != "[1.234567890123456789,1e400,null,false]" {
		t.Fatalf("numbers changed: %s", data)
	}
}

func TestFunctionNumbersRoundTrip(t *testing.T) {
	var req GenerateContentRequest
	body := `{"contents":[{"role":"model","parts":{"function_call":{"name":"lookup","args":` + preciseArguments + `}}},{"role":"user","parts":{"function_response":{"name":"lookup","response":` + preciseArguments + `}}}],"tools":{"function_declarations":{"name":"lookup","parameters_json_schema":` + preciseArguments + `}}}`
	if err := decodeRequest(strings.NewReader(body), &req); err != nil {
		t.Fatal(err)
	}
	requirePreciseArguments(t, req.Contents[0].Parts[0].FunctionCall.Args)
	requirePreciseArguments(t, req.Contents[1].Parts[0].FunctionResponse.Response)
	requirePreciseArguments(t, req.Tools[0].FunctionDeclarations[0].ParametersJsonSchema)
	messages, err := toMessages(nil, req.Contents)
	if err != nil {
		t.Fatal(err)
	}
	requirePreciseArguments(t, jsontext.Value(messages[0].ToolCalls()[0].Arguments))
	requirePreciseArguments(t, toContent(messages[0].Content).Parts[0].FunctionCall.Args)
}

func TestStreamingFunctionNumbers(t *testing.T) {
	var call *FunctionCall
	acc := NewStreamingAccumulator("r1", "test", func(response GenerateContentResponse) error {
		for _, candidate := range response.Candidates {
			if candidate.Content != nil {
				for _, part := range candidate.Content.Parts {
					if part.FunctionCall != nil {
						call = part.FunctionCall
					}
				}
			}
		}
		return nil
	})
	for _, fragment := range []string{`{"id":9007199254740993,`, `"values":[1.234567890123456789,1e400,null,false]}`} {
		if err := acc.Add(provider.Completion{Message: &provider.Message{Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "lookup", Arguments: fragment})}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatal(err)
	}
	if call == nil {
		t.Fatal("missing function call")
	}
	requirePreciseArguments(t, call.Args)
}

// Tool inputs are objects on the wire but strings in provider history. Repeated
// decoding must preserve the exact text to keep cached prefixes stable.
func TestToolArgumentsStableAcrossReplay(t *testing.T) {
	const input = `{"path":"note.txt","operation":{"z":2,"a":1},"content":"hello"}`
	const want = `{"content":"hello","operation":{"a":1,"z":2},"path":"note.txt"}`
	for range 100 {
		var args map[string]any
		if err := json.Unmarshal([]byte(input), &args); err != nil {
			t.Fatal(err)
		}
		got, err := toJSONString(args)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("replay arguments = %s, want %s", got, want)
		}
	}
}
