package custom

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/tool"
	"google.golang.org/grpc"
)

type fakeToolClient struct {
	definitions *ToolsResponse
	response    *ResultResponse
	err         error
	request     *ExecuteRequest
}

func (f *fakeToolClient) Tools(context.Context, *ToolsRequest, ...grpc.CallOption) (*ToolsResponse, error) {
	return f.definitions, f.err
}
func (f *fakeToolClient) Execute(_ context.Context, request *ExecuteRequest, _ ...grpc.CallOption) (*ResultResponse, error) {
	f.request = request
	return f.response, f.err
}

func TestTools_ValidatesDefinitions(t *testing.T) {
	for _, test := range []struct {
		definition *Definition
		invalid    bool
	}{
		{&Definition{Name: "lookup", Parameters: `{"properties":{"query":{"type":"string"}},"required":["query"]}`}, false},
		{&Definition{Name: "noop"}, false}, {nil, true}, {&Definition{}, true},
		{&Definition{Name: "broken", Parameters: "{"}, true},
		{&Definition{Name: "array", Parameters: `{"type":"array"}`}, true},
		{&Definition{Name: "boolean", Parameters: "true"}, true},
		{&Definition{Name: "null", Parameters: "null"}, true},
	} {
		c := &Client{client: &fakeToolClient{definitions: &ToolsResponse{Definitions: []*Definition{test.definition}}}}
		got, err := c.Tools(t.Context())
		if (err != nil) != test.invalid {
			t.Fatalf("definition %+v: got %+v, err %v", test.definition, got, err)
		}
		if !test.invalid && (len(got) != 1 || got[0].Parameters["type"] != "object") {
			t.Fatalf("invalid tools: %+v", got)
		}
	}
}

func TestExecute_DecodesJSONAndPreservesPlainText(t *testing.T) {
	for _, test := range []struct {
		data string
		want any
	}{
		{`{"answer":42}`, map[string]any{"answer": float64(42)}}, {`["a","b"]`, []any{"a", "b"}},
		{`"Report\nwith citations"`, "Report\nwith citations"},
		{"42", float64(42)}, {"true", true}, {"null", nil},
		{"Plain text\nwith a source", "Plain text\nwith a source"},
		{"answer: yes", map[string]any{"answer": "yes"}},
	} {
		f := &fakeToolClient{response: &ResultResponse{Data: test.data}}
		c := &Client{client: f}
		got, err := c.Execute(t.Context(), "lookup", map[string]any{"q": "x"})
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("data %q: got %#v, err %v", test.data, got, err)
		}
		if f.request.Name != "lookup" || f.request.Parameters != `{"q":"x"}` {
			t.Fatalf("request %+v", f.request)
		}
		result, err := tool.RenderResult(c, "lookup", got)
		if err != nil || len(result.Parts) != 1 {
			t.Fatalf("result %+v, err %v", result, err)
		}
		if text, ok := test.want.(string); ok && result.Parts[0].Text != text {
			t.Fatal("text was quoted")
		}
	}
	f := &fakeToolClient{response: &ResultResponse{Data: "ok"}}
	c := &Client{client: f}
	c.Execute(t.Context(), "noop", nil)
	if f.request.Parameters != "{}" {
		t.Fatalf("no-argument payload %q", f.request.Parameters)
	}
}

func TestCustomTool_Errors(t *testing.T) {
	for _, endpoint := range []string{"", "http://example.com", "grpc://", " grpc:// "} {
		if _, err := New(endpoint); err == nil {
			t.Fatalf("accepted endpoint %q", endpoint)
		}
	}
	f := &fakeToolClient{}
	c := &Client{client: f}
	if _, err := c.Tools(t.Context()); err == nil {
		t.Fatal("accepted nil tools response")
	}
	if _, err := c.Execute(t.Context(), "lookup", nil); err == nil {
		t.Fatal("accepted nil execution response")
	}
	f.request = nil
	if _, err := c.Execute(t.Context(), "", nil); err == nil || f.request != nil {
		t.Fatal("blank name reached provider")
	}
	if _, err := c.Execute(t.Context(), "lookup", map[string]any{"bad": make(chan int)}); err == nil || f.request != nil {
		t.Fatal("invalid JSON reached provider")
	}
	f.err = errors.New("RPC failed")
	if _, err := c.Tools(t.Context()); !errors.Is(err, f.err) {
		t.Fatalf("lost tools error: %v", err)
	}
	if _, err := c.Execute(t.Context(), "lookup", nil); !errors.Is(err, f.err) {
		t.Fatalf("lost execution error: %v", err)
	}
}
