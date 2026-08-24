package responses

import (
	"errors"
	"fmt"
	"testing"

	"github.com/adrianliechti/wingman/server/openai/shared"
)

func TestToTools_RejectsNamelessTools(t *testing.T) {
	tests := []struct {
		name  string
		tools []Tool
		param string
	}{
		{name: "function", tools: []Tool{{Type: ToolTypeFunction}}, param: "tools[0].name"},
		{name: "custom", tools: []Tool{{Type: ToolTypeCustom}}, param: "tools[0].name"},
		{name: "namespace", tools: []Tool{{Type: ToolTypeNamespace}}, param: "tools[0].name"},
		{name: "namespace inner", tools: []Tool{{Type: ToolTypeNamespace, Name: "ns", Tools: []Tool{{Type: ToolTypeFunction}}}}, param: "tools[0].tools[0].name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := toTools(tt.tools)
			if err == nil {
				t.Fatal("expected error for nameless tool")
			}

			var invalid *shared.Error
			if !errors.As(err, &invalid) {
				t.Fatalf("expected shared.Error, got %T: %v", err, err)
			}
			if invalid.Param != tt.param {
				t.Errorf("param = %q, want %q", invalid.Param, tt.param)
			}
			if invalid.Code != "missing_required_parameter" {
				t.Errorf("code = %q, want missing_required_parameter", invalid.Code)
			}
			if invalid.Message != fmt.Sprintf("Missing required parameter: '%s'.", tt.param) {
				t.Errorf("message = %q", invalid.Message)
			}
		})
	}
}
