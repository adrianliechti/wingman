package tool

import (
	"context"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type resultProvider struct{}

func (*resultProvider) Tools(context.Context) ([]Tool, error)                        { return nil, nil }
func (*resultProvider) Execute(context.Context, string, map[string]any) (any, error) { return nil, nil }

type richProvider struct {
	resultProvider
	result provider.ToolResult
}

func (p *richProvider) Result(string, any) provider.ToolResult { return p.result }

func TestRenderResult_PreservesTextAndContentParts(t *testing.T) {
	for _, test := range []struct {
		value any
		want  string
	}{{"Report\nwith citations", "Report\nwith citations"}, {map[string]any{"count": 2}, `{"count":2}`}, {nil, "null"}} {
		got, err := RenderResult(&resultProvider{}, "test", test.value)
		if err != nil || !reflect.DeepEqual(got, TextResult(test.want)) {
			t.Fatalf("value %v: got %+v, err %v", test.value, got, err)
		}
	}
	want := provider.ToolResult{ID: "original", IsError: true, Parts: []provider.Part{
		{Text: "caption"}, {File: &provider.File{ContentType: "image/png", Content: []byte{1}}},
	}}
	got, err := RenderResult(&richProvider{result: want}, "test", "opaque")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("lost result fields: %+v, err %v", got, err)
	}
	if _, err := RenderResult(&resultProvider{}, "test", make(chan int)); err == nil {
		t.Fatal("ignored an encoding failure")
	}
}
