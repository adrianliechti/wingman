package gemini

import (
	"strings"
	"testing"
)

// Google's shell examples use protobuf snake_case names and singleton
// objects for repeated fields; both must decode like the canonical form.
func TestDecodeRequestProtobufForms(t *testing.T) {
	body := `{
		"system_instruction": {"parts": {"text": "You are a cat."}},
		"contents": {"role": "user", "parts": {"text": "Hello there"}},
		"generation_config": {
			"max_output_tokens": 50,
			"stop_sequences": "zzz",
			"response_mime_type": "application/json",
			"response_schema": {"type": "OBJECT", "properties": {"first_name": {"type": "STRING"}}}
		},
		"tools": {"function_declarations": {"name": "get_weather", "parameters": {"type": "object", "properties": {"city_name": {"type": "string"}}}}},
		"tool_config": {"function_calling_config": {"mode": "ANY", "allowed_function_names": "get_weather"}}
	}`

	var req GenerateContentRequest
	if err := decodeRequest(strings.NewReader(body), &req); err != nil {
		t.Fatal(err)
	}

	if req.SystemInstruction == nil || len(req.SystemInstruction.Parts) != 1 || req.SystemInstruction.Parts[0].Text != "You are a cat." {
		t.Errorf("system instruction: %+v", req.SystemInstruction)
	}
	if len(req.Contents) != 1 || len(req.Contents[0].Parts) != 1 || req.Contents[0].Parts[0].Text != "Hello there" {
		t.Errorf("contents: %+v", req.Contents)
	}

	gc := req.GenerationConfig
	if gc == nil || gc.MaxOutputTokens == nil || *gc.MaxOutputTokens != 50 || len(gc.StopSequences) != 1 || gc.ResponseMimeType != "application/json" {
		t.Errorf("generation config: %+v", gc)
	}

	// caller schemas keep their own keys
	schema, _ := gc.ResponseSchema.(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["first_name"]; !ok {
		t.Errorf("response schema keys were rewritten: %v", schema)
	}

	if len(req.Tools) != 1 || len(req.Tools[0].FunctionDeclarations) != 1 || req.Tools[0].FunctionDeclarations[0].Name != "get_weather" {
		t.Fatalf("tools: %+v", req.Tools)
	}
	params, _ := req.Tools[0].FunctionDeclarations[0].Parameters.(map[string]any)
	pprops, _ := params["properties"].(map[string]any)
	if _, ok := pprops["city_name"]; !ok {
		t.Errorf("parameter keys were rewritten: %v", params)
	}

	fcc := req.ToolConfig.FunctionCallingConfig
	if fcc == nil || fcc.Mode != "ANY" || len(fcc.AllowedFunctionNames) != 1 || fcc.AllowedFunctionNames[0] != "get_weather" {
		t.Errorf("tool config: %+v", fcc)
	}
}

func TestDecodeRequestSystemInstructionString(t *testing.T) {
	var req GenerateContentRequest
	if err := decodeRequest(strings.NewReader(`{"systemInstruction": "Be brief.", "contents": []}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.SystemInstruction == nil || req.SystemInstruction.Parts[0].Text != "Be brief." {
		t.Errorf("system instruction: %+v", req.SystemInstruction)
	}
}

func TestDecodeRequestKeepsFunctionData(t *testing.T) {
	body := `{"contents": [{"role": "model", "parts": [{"functionCall": {"name": "f", "args": {"snake_key": 1}}}]},
	                       {"role": "user", "parts": [{"functionResponse": {"name": "f", "response": {"result_value": "ok"}}}]}]}`

	var req GenerateContentRequest
	if err := decodeRequest(strings.NewReader(body), &req); err != nil {
		t.Fatal(err)
	}
	if _, ok := req.Contents[0].Parts[0].FunctionCall.Args["snake_key"]; !ok {
		t.Errorf("function args were rewritten: %v", req.Contents[0].Parts[0].FunctionCall.Args)
	}
	if _, ok := req.Contents[1].Parts[0].FunctionResponse.Response["result_value"]; !ok {
		t.Errorf("function response was rewritten: %v", req.Contents[1].Parts[0].FunctionResponse.Response)
	}
}
