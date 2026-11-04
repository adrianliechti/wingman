package anthropic

import (
	"encoding/json/v2"
	"testing"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
)

func TestThinkingPresenceV2(t *testing.T) {
	data, err := json.Marshal(ContentBlock{Type: "thinking"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["thinking"] != "" || fields["signature"] != "" {
		t.Fatalf("lost required empty fields: %s", data)
	}
	if _, ok := fields["text"]; ok {
		t.Fatalf("included unrelated field: %s", data)
	}
}

func TestContentSourceUnionV2(t *testing.T) {
	var block ContentBlockParam
	if err := jsonutil.Unmarshal([]byte(`{"source":"https://example.com","type":"search_result"}`), &block); err != nil {
		t.Fatal(err)
	}
	if block.SearchSource != "https://example.com" || block.Source != nil {
		t.Fatalf("invalid search source: %+v", block)
	}
	if err := jsonutil.Unmarshal([]byte(`{"source":{"type":"url","url":"https://example.com/image.png"},"type":"image"}`), &block); err != nil {
		t.Fatal(err)
	}
	if block.Source == nil || block.Source.URL != "https://example.com/image.png" || block.SearchSource != "" {
		t.Fatalf("invalid image source: %+v", block)
	}
	if err := jsonutil.Unmarshal([]byte(`{"source":42,"type":"image"}`), &block); err == nil {
		t.Fatal("accepted invalid source")
	}
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
