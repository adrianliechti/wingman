package responses

import (
	"encoding/json"
	"errors"
	"fmt"
)

// https://platform.openai.com/docs/api-reference/responses/create
type ResponsesRequest struct {
	Model string `json:"model,omitempty"`

	Stream bool `json:"stream,omitempty"`

	Instructions string `json:"instructions,omitempty"`

	Input ResponsesInput `json:"input"`

	Include []string `json:"include,omitempty"`

	Tools []Tool `json:"tools,omitempty"`

	Text *TextConfig `json:"text,omitempty"`

	MaxOutputTokens *int     `json:"max_output_tokens,omitempty"`
	Temperature     *float32 `json:"temperature,omitempty"`

	Reasoning *ReasoningConfig `json:"reasoning,omitempty"`

	ContextManagement []ContextManagementConfig `json:"context_management,omitempty"`

	ToolChoice        *ToolChoice `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool       `json:"parallel_tool_calls,omitempty"`

	Truncation string `json:"truncation,omitempty"`
}

// ContextManagementConfig represents a context management entry
type ContextManagementConfig struct {
	Type string `json:"type"`

	CompactThreshold *int64 `json:"compact_threshold,omitempty"`
}

// ReasoningConfig contains configuration options for reasoning models
type ReasoningConfig struct {
	Effort  *ReasoningEffort `json:"effort"`
	Summary *any             `json:"summary"`
	Context *string          `json:"context,omitempty"`
}

type ReasoningEffort string

var (
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortXHigh   ReasoningEffort = "xhigh"
	ReasoningEffortMax     ReasoningEffort = "max"
)

// TextConfig represents configuration options for text responses
type TextConfig struct {
	Format    *TextFormat `json:"format,omitempty"`
	Verbosity Verbosity   `json:"verbosity,omitempty"`
}

type Verbosity string

var (
	VerbosityLow    Verbosity = "low"
	VerbosityMedium Verbosity = "medium"
	VerbosityHigh   Verbosity = "high"
)

// TextFormat represents the format configuration for text output
type TextFormat struct {
	Type string `json:"type,omitempty"` // "text", "json_object", or "json_schema"

	// For json_schema type
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
}

// ToolType represents the type of tool
type ToolType string

const (
	ToolTypeFunction   ToolType = "function"
	ToolTypeCustom     ToolType = "custom"
	ToolTypeApplyPatch ToolType = "apply_patch"
	ToolTypeComputer   ToolType = "computer"
	ToolTypeShell      ToolType = "shell"
	ToolTypeLocalShell ToolType = "local_shell"
	ToolTypeNamespace  ToolType = "namespace"
	ToolTypeToolSearch ToolType = "tool_search"
)

// Tool represents a tool in the request
type Tool struct {
	Type ToolType `json:"type"`

	// For function tools
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`

	Strict     *bool          `json:"strict,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`

	// For namespace tools (groups of function/custom tools).
	Tools []Tool `json:"tools,omitempty"`

	// For custom tools (like apply_patch)
	Format *CustomToolFormat `json:"format,omitempty"`

	// For tool_search tools: "server" or "client".
	Execution string `json:"execution,omitempty"`

	// For function/custom tools paired with a tool_search tool: hide from the
	// initial tool list, surface via tool_search instead.
	DeferLoading *bool `json:"defer_loading,omitempty"`

	// For computer tool
	DisplayWidth  int    `json:"display_width,omitempty"`
	DisplayHeight int    `json:"display_height,omitempty"`
	Environment   string `json:"environment,omitempty"` // "browser", "ubuntu", "windows", "mac"
}

type ToolChoice struct {
	Mode ToolChoiceMode `json:"mode,omitempty"`

	AllowedTools []ToolChoiceAllowedTool `json:"allowed_tools,omitempty"`

	// Hosted captures typed tool_choice objects for hosted tools, e.g.
	// {"type":"file_search"} or {"type":"mcp","server_label":"x","name":"y"}.
	// When set, Mode is also Auto so dispatch falls back to the auto path.
	Hosted *HostedToolChoice `json:"-"`
}

type ToolChoiceMode string

const (
	ToolChoiceModeNone     ToolChoiceMode = "none"
	ToolChoiceModeAuto     ToolChoiceMode = "auto"
	ToolChoiceModeRequired ToolChoiceMode = "required"
)

type ToolChoiceAllowedTool struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// HostedToolChoice is the wire shape for typed hosted-tool choices like
// {"type":"file_search"} or {"type":"mcp","server_label":"x","name":"y"}.
type HostedToolChoice struct {
	Type        string `json:"type"`
	ServerLabel string `json:"server_label,omitempty"`
	Name        string `json:"name,omitempty"`
}

var hostedToolChoiceTypes = map[string]struct{}{
	"file_search":          {},
	"web_search":           {},
	"web_search_preview":   {},
	"computer":             {},
	"computer_use_preview": {},
	"image_generation":     {},
	"code_interpreter":     {},
	"mcp":                  {},
}

func (t *ToolChoice) UnmarshalJSON(data []byte) error {
	var mode string

	if err := json.Unmarshal(data, &mode); err == nil {
		t.Mode = ToolChoiceMode(mode)
		return nil
	}

	var function struct {
		Type string `json:"type"`
		Name string `json:"name,omitempty"`
	}

	if err := json.Unmarshal(data, &function); err == nil && function.Type == "function" && function.Name != "" {
		t.Mode = ToolChoiceModeRequired
		t.AllowedTools = []ToolChoiceAllowedTool{{
			Type: function.Type,
			Name: function.Name,
		}}
		return nil
	}

	// Handle {"type":"allowed_tools","mode":"...","tools":[...]} format from OpenAI SDK
	var allowedTools struct {
		Type string `json:"type"`

		Mode  ToolChoiceMode          `json:"mode"`
		Tools []ToolChoiceAllowedTool `json:"tools"`
	}

	if err := json.Unmarshal(data, &allowedTools); err == nil && allowedTools.Type == "allowed_tools" {
		t.Mode = allowedTools.Mode
		t.AllowedTools = allowedTools.Tools
		return nil
	}

	// Handle typed hosted-tool choices: {"type":"file_search"|"web_search"|...|"mcp"[,"server_label"][,"name"]}.
	var hosted HostedToolChoice

	if err := json.Unmarshal(data, &hosted); err == nil {
		if _, ok := hostedToolChoiceTypes[hosted.Type]; ok {
			t.Mode = ToolChoiceModeAuto
			t.Hosted = &hosted
			return nil
		}
	}

	type alias ToolChoice

	var value alias

	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}

	*t = ToolChoice(value)

	return nil
}

// CustomToolFormat describes the format for custom tools
type CustomToolFormat struct {
	Type       string `json:"type,omitempty"`       // "grammar"
	Syntax     string `json:"syntax,omitempty"`     // "lark"
	Definition string `json:"definition,omitempty"` // the grammar definition
}

// InputItemType represents the type of input item
type InputItemType string

const (
	InputItemTypeMessage              InputItemType = "message"
	InputItemTypeReasoning            InputItemType = "reasoning"
	InputItemTypeCompaction           InputItemType = "compaction"
	InputItemTypeFunctionCall         InputItemType = "function_call"
	InputItemTypeFunctionCallOutput   InputItemType = "function_call_output"
	InputItemTypeApplyPatchCall       InputItemType = "apply_patch_call"
	InputItemTypeApplyPatchCallOutput InputItemType = "apply_patch_call_output"
	InputItemTypeCustomToolCall       InputItemType = "custom_tool_call"
	InputItemTypeCustomToolCallOutput InputItemType = "custom_tool_call_output"
	InputItemTypeComputerCall         InputItemType = "computer_call"
	InputItemTypeComputerCallOutput   InputItemType = "computer_call_output"
	InputItemTypeShellCall            InputItemType = "shell_call"
	InputItemTypeShellCallOutput      InputItemType = "shell_call_output"
	InputItemTypeLocalShellCall       InputItemType = "local_shell_call"
	InputItemTypeLocalShellCallOutput InputItemType = "local_shell_call_output"
	InputItemTypeToolSearchCall       InputItemType = "tool_search_call"
	InputItemTypeToolSearchOutput     InputItemType = "tool_search_output"
)

type ResponsesInput struct {
	Items []InputItem `json:"-"`
}

// InputItem represents a single item in the input array
type InputItem struct {
	Type InputItemType `json:"type,omitempty"`

	// For message type
	*InputMessage

	// For reasoning type
	*InputReasoning

	// For compaction type
	*InputCompaction

	// For function_call type
	*InputFunctionCall

	// For function_call_output type
	*InputFunctionCallOutput

	// For apply_patch_call type
	*InputApplyPatchCall

	// For apply_patch_call_output type
	*InputApplyPatchCallOutput

	// For custom_tool_call type
	*InputCustomToolCall

	// For custom_tool_call_output type
	*InputCustomToolCallOutput

	// For computer_call type
	*InputComputerCall

	// For computer_call_output type
	*InputComputerCallOutput

	// For shell_call and local_shell_call types
	*InputShellCall

	// For shell_call_output and local_shell_call_output types
	*InputShellCallOutput

	// For tool_search_call type
	*InputToolSearchCall

	// For tool_search_output type
	*InputToolSearchOutput
}

// InputComputerCall represents a computer call in the input (for multi-turn)
type InputComputerCall struct {
	ID      string `json:"id,omitempty"`
	CallID  string `json:"call_id,omitempty"`
	Status  string `json:"status,omitempty"`
	Actions []any  `json:"actions,omitempty"`

	PendingSafetyChecks []SafetyCheck `json:"pending_safety_checks,omitempty"`
}

// InputComputerCallOutput represents the result of a computer call
type InputComputerCallOutput struct {
	CallID string `json:"call_id,omitempty"`
	Output any    `json:"output,omitempty"`
	Status string `json:"status,omitempty"`

	AcknowledgedSafetyChecks []SafetyCheck `json:"acknowledged_safety_checks,omitempty"`
}

type SafetyCheck struct {
	ID      string `json:"id,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// InputShellCall represents a shell_call or local_shell_call in the input
// (for multi-turn). The action carries either {commands, timeout_ms,
// max_output_length} (shell) or {command, env, timeout_ms, user,
// working_directory} (local_shell).
type InputShellCall struct {
	ID     string          `json:"id,omitempty"`
	CallID string          `json:"call_id,omitempty"`
	Status string          `json:"status,omitempty"`
	Action json.RawMessage `json:"action,omitempty"`
}

// InputShellCallOutput represents a shell_call_output or
// local_shell_call_output. Output is a content list for shell and a plain
// JSON string for local_shell.
type InputShellCallOutput struct {
	ID     string          `json:"id,omitempty"`
	CallID string          `json:"call_id,omitempty"`
	Status string          `json:"status,omitempty"`
	Output json.RawMessage `json:"output,omitempty"`
}

// InputApplyPatchCall represents an apply_patch call in the input (for multi-turn)
type InputApplyPatchCall struct {
	ID        string              `json:"id,omitempty"`
	CallID    string              `json:"call_id,omitempty"`
	Status    string              `json:"status,omitempty"`
	Operation ApplyPatchOperation `json:"operation,omitempty"`
}

// InputApplyPatchCallOutput represents the result of an apply_patch call
type InputApplyPatchCallOutput struct {
	CallID string         `json:"call_id,omitempty"`
	Output []InputContent `json:"output,omitempty"`
	Status string         `json:"status,omitempty"`
}

func (o *InputApplyPatchCallOutput) UnmarshalJSON(data []byte) error {
	var raw struct {
		CallID string          `json:"call_id,omitempty"`
		Output json.RawMessage `json:"output,omitempty"`
		Status string          `json:"status,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	o.CallID = raw.CallID
	o.Status = raw.Status
	output, err := unmarshalInputOutput(raw.Output)
	if err != nil {
		return err
	}
	o.Output = output
	return nil
}

// InputToolSearchCall represents a prior tool_search call (codex echoes
// these back on multi-turn). Arguments is raw JSON because the model is free
// to choose its own argument shape.
type InputToolSearchCall struct {
	ID        string          `json:"id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Status    string          `json:"status,omitempty"`
	Execution string          `json:"execution,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// InputToolSearchOutput represents the tools returned by the client (or the
// server, for hosted tool_search) for a prior tool_search_call. Tools is the
// raw JSON array of tool definitions so it passes through unmodified.
type InputToolSearchOutput struct {
	ID        string          `json:"id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Status    string          `json:"status,omitempty"`
	Execution string          `json:"execution,omitempty"`
	Tools     json.RawMessage `json:"tools,omitempty"`
}

// InputCustomToolCall represents a custom (freeform grammar) tool call in the
// input. Codex registers apply_patch as a custom tool, so its prior calls come
// back as custom_tool_call items whose Input is the raw patch envelope.
type InputCustomToolCall struct {
	ID     string `json:"id,omitempty"`
	CallID string `json:"call_id,omitempty"`
	Name   string `json:"name,omitempty"`
	Input  string `json:"input,omitempty"`
	Status string `json:"status,omitempty"`
}

// InputCustomToolCallOutput represents the result of a custom tool call.
type InputCustomToolCallOutput struct {
	CallID string         `json:"call_id,omitempty"`
	Name   string         `json:"name,omitempty"`
	Output []InputContent `json:"output,omitempty"`
	Status string         `json:"status,omitempty"`
}

func (o *InputCustomToolCallOutput) UnmarshalJSON(data []byte) error {
	var raw struct {
		CallID string          `json:"call_id,omitempty"`
		Name   string          `json:"name,omitempty"`
		Output json.RawMessage `json:"output,omitempty"`
		Status string          `json:"status,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	o.CallID = raw.CallID
	o.Name = raw.Name
	o.Status = raw.Status
	output, err := unmarshalInputOutput(raw.Output)
	if err != nil {
		return err
	}
	o.Output = output
	return nil
}

// InputReasoning represents a reasoning item in the input
type InputReasoning struct {
	ID               string                 `json:"id,omitempty"`
	Summary          []ReasoningSummaryPart `json:"summary,omitempty"`
	Content          json.RawMessage        `json:"content,omitempty"`           // Can be null
	EncryptedContent string                 `json:"encrypted_content,omitempty"` // Base64 encoded
}

// InputCompaction represents a compaction item in the input
type InputCompaction struct {
	ID               string `json:"id,omitempty"`
	Content          string `json:"content,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

// ReasoningSummaryPart represents a part of the reasoning summary
type ReasoningSummaryPart struct {
	Type string `json:"type,omitempty"` // "summary_text"
	Text string `json:"text,omitempty"`
}

// InputFunctionCall represents a function call in the input
type InputFunctionCall struct {
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Status    string `json:"status,omitempty"` // "in_progress", "completed"
}

// InputFunctionCallOutput represents a function call output in the input
type InputFunctionCallOutput struct {
	CallID string         `json:"call_id,omitempty"`
	Output []InputContent `json:"output,omitempty"`
}

func (o *InputFunctionCallOutput) UnmarshalJSON(data []byte) error {
	var raw struct {
		CallID string          `json:"call_id,omitempty"`
		Output json.RawMessage `json:"output,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	o.CallID = raw.CallID
	output, err := unmarshalInputOutput(raw.Output)
	if err != nil {
		return err
	}
	o.Output = output
	return nil
}

// unmarshalInputOutput accepts the polymorphic `output` field defined by
// the OpenAI Responses API: either a plain string, or an array of content
// parts. A string is normalized to a single output_text part.
func unmarshalInputOutput(raw json.RawMessage) ([]InputContent, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []InputContent{{Type: OutputContentText, Text: s}}, nil
	}
	var parts []InputContent
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("output must be string or array of content parts: %w", err)
	}
	return parts, nil
}

func (ri *ResponsesInput) UnmarshalJSON(data []byte) error {
	// Try string input first
	var stringInput string
	if err := json.Unmarshal(data, &stringInput); err == nil {
		ri.Items = []InputItem{
			{
				Type: InputItemTypeMessage,
				InputMessage: &InputMessage{
					Role: MessageRoleUser,
					Content: []InputContent{
						{
							Type: InputContentText,
							Text: stringInput,
						},
					},
				},
			},
		}
		return nil
	}

	// Try array of input items
	var rawItems []json.RawMessage
	if err := json.Unmarshal(data, &rawItems); err != nil {
		return errors.New("failed to unmarshal ResponsesInput")
	}

	ri.Items = make([]InputItem, 0, len(rawItems))

	for _, raw := range rawItems {
		// First, determine the type
		var typeWrapper struct {
			Type InputItemType `json:"type"`
		}
		if err := json.Unmarshal(raw, &typeWrapper); err != nil {
			return err
		}

		item := InputItem{Type: typeWrapper.Type}

		switch typeWrapper.Type {
		case InputItemTypeMessage, "":
			// Default to message type for backwards compatibility
			var msg InputMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				return err
			}
			item.Type = InputItemTypeMessage
			item.InputMessage = &msg

		case InputItemTypeReasoning:
			var reasoning InputReasoning
			if err := json.Unmarshal(raw, &reasoning); err != nil {
				return err
			}
			item.InputReasoning = &reasoning

		case InputItemTypeCompaction:
			var compaction InputCompaction
			if err := json.Unmarshal(raw, &compaction); err != nil {
				return err
			}
			item.InputCompaction = &compaction

		case InputItemTypeFunctionCall:
			var fc InputFunctionCall
			if err := json.Unmarshal(raw, &fc); err != nil {
				return err
			}
			item.InputFunctionCall = &fc

		case InputItemTypeFunctionCallOutput:
			var fco InputFunctionCallOutput
			if err := json.Unmarshal(raw, &fco); err != nil {
				return err
			}
			item.InputFunctionCallOutput = &fco

		case InputItemTypeApplyPatchCall:
			var apc InputApplyPatchCall
			if err := json.Unmarshal(raw, &apc); err != nil {
				return err
			}
			item.InputApplyPatchCall = &apc

		case InputItemTypeApplyPatchCallOutput:
			var apco InputApplyPatchCallOutput
			if err := json.Unmarshal(raw, &apco); err != nil {
				return err
			}
			item.InputApplyPatchCallOutput = &apco

		case InputItemTypeCustomToolCall:
			var ctc InputCustomToolCall
			if err := json.Unmarshal(raw, &ctc); err != nil {
				return err
			}
			item.InputCustomToolCall = &ctc

		case InputItemTypeCustomToolCallOutput:
			var ctco InputCustomToolCallOutput
			if err := json.Unmarshal(raw, &ctco); err != nil {
				return err
			}
			item.InputCustomToolCallOutput = &ctco

		case InputItemTypeComputerCall:
			var cc InputComputerCall
			if err := json.Unmarshal(raw, &cc); err != nil {
				return err
			}
			item.InputComputerCall = &cc

		case InputItemTypeComputerCallOutput:
			var cco InputComputerCallOutput
			if err := json.Unmarshal(raw, &cco); err != nil {
				return err
			}
			item.InputComputerCallOutput = &cco

		case InputItemTypeShellCall, InputItemTypeLocalShellCall:
			var sc InputShellCall
			if err := json.Unmarshal(raw, &sc); err != nil {
				return err
			}
			item.InputShellCall = &sc

		case InputItemTypeShellCallOutput, InputItemTypeLocalShellCallOutput:
			var sco InputShellCallOutput
			if err := json.Unmarshal(raw, &sco); err != nil {
				return err
			}
			item.InputShellCallOutput = &sco

		case InputItemTypeToolSearchCall:
			var tsc InputToolSearchCall
			if err := json.Unmarshal(raw, &tsc); err != nil {
				return err
			}
			item.InputToolSearchCall = &tsc

		case InputItemTypeToolSearchOutput:
			var tso InputToolSearchOutput
			if err := json.Unmarshal(raw, &tso); err != nil {
				return err
			}
			item.InputToolSearchOutput = &tso

		default:
			return fmt.Errorf("unknown input item type: %s", typeWrapper.Type)
		}

		ri.Items = append(ri.Items, item)
	}

	return nil
}

type MessageRole string

var (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleSystem    MessageRole = "system"
	MessageRoleDeveloper MessageRole = "developer"
)

type InputMessage struct {
	Role MessageRole `json:"role,omitempty"`

	Content []InputContent `json:"content,omitempty"`
}

func (im *InputMessage) UnmarshalJSON(data []byte) error {
	var stringInput string

	if err := json.Unmarshal(data, &stringInput); err == nil {
		im.Role = MessageRoleUser

		im.Content = []InputContent{
			{
				Type: InputContentText,
				Text: stringInput,
			},
		}

		return nil
	}

	var textInput struct {
		Role MessageRole `json:"role"`
		Text string      `json:"text"`
	}

	if err := json.Unmarshal(data, &textInput); err == nil && textInput.Role != "" && textInput.Text != "" {
		im.Role = textInput.Role

		im.Content = []InputContent{
			{
				Type: InputContentText,
				Text: textInput.Text,
			},
		}

		return nil
	}

	var messageInput struct {
		Role    MessageRole `json:"role"`
		Content any         `json:"content"`
	}

	if err := json.Unmarshal(data, &messageInput); err == nil {
		im.Role = messageInput.Role

		switch content := messageInput.Content.(type) {
		case string:
			im.Content = []InputContent{
				{
					Type: InputContentText,
					Text: content,
				},
			}

			return nil

		case []any:
			data, err := json.Marshal(content)

			if err != nil {
				return err
			}

			if err := json.Unmarshal(data, &im.Content); err != nil {
				return err
			}

			return nil

		default:
			return fmt.Errorf("unsupported content type: %T", content)
		}
	}

	return errors.New("failed to unmarshal InputMessage")
}

type InputContent struct {
	Type InputContentType `json:"type,omitempty"`
	Text string           `json:"text,omitempty"`

	ImageURL string `json:"image_url,omitempty"`

	Filename string `json:"filename,omitempty"`
	FileURL  string `json:"file_url,omitempty"`
	FileData string `json:"file_data,omitempty"`
}

type InputContentType string

const (
	InputContentText  InputContentType = "input_text"
	InputContentImage InputContentType = "input_image"
	InputContentFile  InputContentType = "input_file"

	OutputContentText InputContentType = "output_text"
)

type Response struct {
	ID     string `json:"id"`
	Object string `json:"object"` // response

	CreatedAt   int64  `json:"created_at"`
	CompletedAt *int64 `json:"completed_at"`

	Model  string `json:"model"`
	Status string `json:"status"` // completed, failed, in_progress, incomplete

	Background bool `json:"background"`

	Output []ResponseOutput `json:"output"`

	Error             *ResponseError `json:"error"`
	IncompleteDetails *any           `json:"incomplete_details"`

	Instructions       *string `json:"instructions"`
	MaxOutputTokens    *int    `json:"max_output_tokens"`
	MaxToolCalls       *int    `json:"max_tool_calls"`
	Metadata           any     `json:"metadata"`
	ParallelToolCalls  bool    `json:"parallel_tool_calls"`
	PreviousResponseID *string `json:"previous_response_id"`

	Reasoning *ReasoningConfig `json:"reasoning"`

	ServiceTier string  `json:"service_tier"`
	Store       bool    `json:"store"`
	Temperature float32 `json:"temperature"`

	Text *TextConfig `json:"text"`

	ToolChoice any   `json:"tool_choice"`
	Tools      []any `json:"tools"`

	TopLogprobs      int     `json:"top_logprobs"`
	TopP             float32 `json:"top_p"`
	Truncation       string  `json:"truncation"`
	FrequencyPenalty float32 `json:"frequency_penalty"`
	PresencePenalty  float32 `json:"presence_penalty"`

	PromptCacheKey       *string `json:"prompt_cache_key"`
	PromptCacheRetention *string `json:"prompt_cache_retention"`

	Moderation *any `json:"moderation"`

	Billing *ResponseBilling `json:"billing,omitempty"`

	SafetyIdentifier *string `json:"safety_identifier"`

	Usage *Usage `json:"usage"`
	User  *any   `json:"user"`
}

// ResponseBilling represents billing information
type ResponseBilling struct {
	Payer string `json:"payer,omitempty"` // "developer"
}

// Usage contains token usage information
type Usage struct {
	InputTokens        int                 `json:"input_tokens"`
	InputTokensDetails *InputTokensDetails `json:"input_tokens_details"`

	OutputTokens        int                  `json:"output_tokens"`
	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details"`

	TotalTokens int `json:"total_tokens"`
}

// InputTokensDetails contains detailed input token breakdown
type InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// OutputTokensDetails contains detailed output token breakdown
type OutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// ResponseError contains error details when a response fails
type ResponseError struct {
	Type    string `json:"type"`            // e.g., "server_error", "invalid_request"
	Code    string `json:"code,omitempty"`  // Optional error code for additional detail
	Param   string `json:"param,omitempty"` // The input parameter related to the error
	Message string `json:"message"`         // Human-readable error message
}

type ResponseOutput struct {
	Type ResponseOutputType `json:"type,omitempty"`

	*OutputMessage
	*FunctionCallOutputItem
	*ApplyPatchCallItem
	*CustomToolCallItem
	*ComputerCallItem
	*ShellCallItem
	*ToolSearchCallItem
	*ReasoningOutputItem
	*CompactionOutputItem
}

// MarshalJSON implements custom JSON marshaling to avoid field conflicts
// between embedded structs (ID, Type, Status fields exist in multiple embedded types)
func (r ResponseOutput) MarshalJSON() ([]byte, error) {
	switch r.Type {
	case ResponseOutputTypeMessage:
		if r.OutputMessage != nil {
			return json.Marshal(struct {
				Type     ResponseOutputType `json:"type"`
				ID       string             `json:"id,omitempty"`
				Role     MessageRole        `json:"role,omitempty"`
				Status   string             `json:"status,omitempty"`
				Contents []OutputContent    `json:"content"`
				Phase    string             `json:"phase,omitempty"`
			}{
				Type:     r.Type,
				ID:       r.OutputMessage.ID,
				Role:     r.OutputMessage.Role,
				Status:   r.OutputMessage.Status,
				Contents: r.OutputMessage.Contents,
				Phase:    r.OutputMessage.Phase,
			})
		}
	case ResponseOutputTypeFunctionCall:
		if r.FunctionCallOutputItem != nil {
			return json.Marshal(struct {
				Type      ResponseOutputType `json:"type"`
				ID        string             `json:"id"`
				Status    string             `json:"status"`
				Name      string             `json:"name"`
				Namespace string             `json:"namespace,omitempty"`
				CallID    string             `json:"call_id"`
				Arguments string             `json:"arguments"`
			}{
				Type:      r.Type,
				ID:        r.FunctionCallOutputItem.ID,
				Status:    r.FunctionCallOutputItem.Status,
				Name:      r.FunctionCallOutputItem.Name,
				Namespace: r.FunctionCallOutputItem.Namespace,
				CallID:    r.FunctionCallOutputItem.CallID,
				Arguments: r.FunctionCallOutputItem.Arguments,
			})
		}
	case ResponseOutputTypeShellCall, ResponseOutputTypeLocalShellCall:
		if r.ShellCallItem != nil {
			return json.Marshal(struct {
				Type   ResponseOutputType `json:"type"`
				ID     string             `json:"id"`
				Status string             `json:"status"`
				CallID string             `json:"call_id"`
				Action json.RawMessage    `json:"action,omitempty"`
			}{
				Type:   r.Type,
				ID:     r.ShellCallItem.ID,
				Status: r.ShellCallItem.Status,
				CallID: r.ShellCallItem.CallID,
				Action: r.ShellCallItem.Action,
			})
		}
	case ResponseOutputTypeComputerCall:
		if r.ComputerCallItem != nil {
			return json.Marshal(struct {
				Type    ResponseOutputType `json:"type"`
				ID      string             `json:"id"`
				Status  string             `json:"status"`
				CallID  string             `json:"call_id"`
				Actions []any              `json:"actions"`

				PendingSafetyChecks []SafetyCheck `json:"pending_safety_checks,omitempty"`
			}{
				Type:    r.Type,
				ID:      r.ComputerCallItem.ID,
				Status:  r.ComputerCallItem.Status,
				CallID:  r.ComputerCallItem.CallID,
				Actions: r.ComputerCallItem.Actions,

				PendingSafetyChecks: r.ComputerCallItem.PendingSafetyChecks,
			})
		}
	case ResponseOutputTypeApplyPatchCall:
		if r.ApplyPatchCallItem != nil {
			return json.Marshal(struct {
				Type      ResponseOutputType  `json:"type"`
				ID        string              `json:"id"`
				Status    string              `json:"status"`
				CallID    string              `json:"call_id"`
				Operation ApplyPatchOperation `json:"operation"`
			}{
				Type:      r.Type,
				ID:        r.ApplyPatchCallItem.ID,
				Status:    r.ApplyPatchCallItem.Status,
				CallID:    r.ApplyPatchCallItem.CallID,
				Operation: r.ApplyPatchCallItem.Operation,
			})
		}
	case ResponseOutputTypeToolSearchCall:
		if r.ToolSearchCallItem != nil {
			return json.Marshal(struct {
				Type      ResponseOutputType `json:"type"`
				ID        string             `json:"id"`
				Status    string             `json:"status"`
				CallID    string             `json:"call_id"`
				Execution string             `json:"execution,omitempty"`
				Arguments json.RawMessage    `json:"arguments,omitempty"`
			}{
				Type:      r.Type,
				ID:        r.ToolSearchCallItem.ID,
				Status:    r.ToolSearchCallItem.Status,
				CallID:    r.ToolSearchCallItem.CallID,
				Execution: r.ToolSearchCallItem.Execution,
				Arguments: r.ToolSearchCallItem.Arguments,
			})
		}
	case ResponseOutputTypeCustomToolCall:
		if r.CustomToolCallItem != nil {
			return json.Marshal(struct {
				Type   ResponseOutputType `json:"type"`
				ID     string             `json:"id"`
				Status string             `json:"status"`
				CallID string             `json:"call_id"`
				Name   string             `json:"name"`
				Input  string             `json:"input"`
			}{
				Type:   r.Type,
				ID:     r.CustomToolCallItem.ID,
				Status: r.CustomToolCallItem.Status,
				CallID: r.CustomToolCallItem.CallID,
				Name:   r.CustomToolCallItem.Name,
				Input:  r.CustomToolCallItem.Input,
			})
		}
	case ResponseOutputTypeReasoning:
		if r.ReasoningOutputItem != nil {
			return json.Marshal(struct {
				Type             ResponseOutputType           `json:"type"`
				ID               string                       `json:"id"`
				Summary          []ReasoningOutputSummary     `json:"summary"`
				Content          []ReasoningOutputContentPart `json:"content"`
				EncryptedContent string                       `json:"encrypted_content,omitempty"`
			}{
				Type:             r.Type,
				ID:               r.ReasoningOutputItem.ID,
				Summary:          r.ReasoningOutputItem.Summary,
				Content:          r.ReasoningOutputItem.Content,
				EncryptedContent: r.ReasoningOutputItem.EncryptedContent,
			})
		}
	case ResponseOutputTypeCompaction:
		if r.CompactionOutputItem != nil {
			return json.Marshal(struct {
				Type             ResponseOutputType `json:"type"`
				ID               string             `json:"id"`
				Content          string             `json:"content,omitempty"`
				EncryptedContent string             `json:"encrypted_content,omitempty"`
			}{
				Type:             r.Type,
				ID:               r.CompactionOutputItem.ID,
				Content:          r.CompactionOutputItem.Content,
				EncryptedContent: r.CompactionOutputItem.EncryptedContent,
			})
		}
	}
	// Fallback: just marshal the type
	return json.Marshal(struct {
		Type ResponseOutputType `json:"type,omitempty"`
	}{Type: r.Type})
}

type ResponseOutputType string

var (
	ResponseOutputTypeMessage        ResponseOutputType = "message"
	ResponseOutputTypeFunctionCall   ResponseOutputType = "function_call"
	ResponseOutputTypeApplyPatchCall ResponseOutputType = "apply_patch_call"
	ResponseOutputTypeCustomToolCall ResponseOutputType = "custom_tool_call"
	ResponseOutputTypeComputerCall   ResponseOutputType = "computer_call"
	ResponseOutputTypeShellCall      ResponseOutputType = "shell_call"
	ResponseOutputTypeLocalShellCall ResponseOutputType = "local_shell_call"
	ResponseOutputTypeToolSearchCall ResponseOutputType = "tool_search_call"
	ResponseOutputTypeReasoning      ResponseOutputType = "reasoning"
	ResponseOutputTypeCompaction     ResponseOutputType = "compaction"
)

// ToolSearchCallItem represents a tool_search call in the output.
type ToolSearchCallItem struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"` // tool_search_call
	CallID    string          `json:"call_id"`
	Status    string          `json:"status"`
	Execution string          `json:"execution,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ComputerCallItem represents a computer use tool call in the output
type ComputerCallItem struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // computer_call
	CallID  string `json:"call_id"`
	Status  string `json:"status"`
	Actions []any  `json:"actions"`

	PendingSafetyChecks []SafetyCheck `json:"pending_safety_checks,omitempty"`
}

// ApplyPatchCallItem represents an apply_patch tool call in the output
type ApplyPatchCallItem struct {
	ID        string              `json:"id"`
	Type      string              `json:"type"` // apply_patch_call
	CallID    string              `json:"call_id"`
	Status    string              `json:"status"`
	Operation ApplyPatchOperation `json:"operation"`
}

type ApplyPatchOperation struct {
	Type string `json:"type"` // "create_file", "update_file", "delete_file"
	Path string `json:"path"`
	Diff string `json:"diff"`
}

// CustomToolCallItem represents a custom (freeform grammar) tool call in the
// output. Used for Codex's apply_patch, where Input is the raw patch envelope.
type CustomToolCallItem struct {
	ID     string `json:"id"`
	Type   string `json:"type"` // custom_tool_call
	CallID string `json:"call_id"`
	Status string `json:"status"`
	Name   string `json:"name"`
	Input  string `json:"input"`
}

type OutputMessage struct {
	ID string `json:"id,omitempty"`

	Role MessageRole `json:"role,omitempty"`

	Status string `json:"status,omitempty"` // completed

	Contents []OutputContent `json:"content"`

	Phase string `json:"phase,omitempty"` // final_answer
}

type OutputContent struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
	Logprobs    []any  `json:"logprobs"`
}

func (c OutputContent) MarshalJSON() ([]byte, error) {
	if c.Type == "refusal" {
		return json.Marshal(struct {
			Type    string `json:"type"`
			Refusal string `json:"refusal"`
		}{
			Type:    "refusal",
			Refusal: c.Text,
		})
	}

	type alias OutputContent
	return json.Marshal(alias(c))
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/created
type ResponseCreatedEvent struct {
	Type           string    `json:"type"` // response.created
	SequenceNumber int       `json:"sequence_number"`
	Response       *Response `json:"response"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/in_progress
type ResponseInProgressEvent struct {
	Type           string    `json:"type"` // response.in_progress
	SequenceNumber int       `json:"sequence_number"`
	Response       *Response `json:"response"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/completed
type ResponseCompletedEvent struct {
	Type           string    `json:"type"` // response.completed
	SequenceNumber int       `json:"sequence_number"`
	Response       *Response `json:"response"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/incomplete
type ResponseIncompleteEvent struct {
	Type           string    `json:"type"` // response.incomplete
	SequenceNumber int       `json:"sequence_number"`
	Response       *Response `json:"response"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/failed
type ResponseFailedEvent struct {
	Type           string    `json:"type"` // response.failed
	SequenceNumber int       `json:"sequence_number"`
	Response       *Response `json:"response"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/output_item/added
type OutputItemAddedEvent struct {
	Type           string      `json:"type"` // response.output_item.added
	SequenceNumber int         `json:"sequence_number"`
	OutputIndex    int         `json:"output_index"`
	Item           *OutputItem `json:"item"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/output_item/done
type OutputItemDoneEvent struct {
	Type           string      `json:"type"` // response.output_item.done
	SequenceNumber int         `json:"sequence_number"`
	OutputIndex    int         `json:"output_index"`
	Item           *OutputItem `json:"item"`
}

type OutputItem struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"` // message
	Status  string          `json:"status"`
	Content []OutputContent `json:"content"`
	Phase   string          `json:"phase,omitempty"`
	Role    MessageRole     `json:"role,omitempty"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/content_part/added
type ContentPartAddedEvent struct {
	Type           string         `json:"type"` // response.content_part.added
	SequenceNumber int            `json:"sequence_number"`
	ItemID         string         `json:"item_id"`
	OutputIndex    int            `json:"output_index"`
	ContentIndex   int            `json:"content_index"`
	Part           *OutputContent `json:"part"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/content_part/done
type ContentPartDoneEvent struct {
	Type           string         `json:"type"` // response.content_part.done
	SequenceNumber int            `json:"sequence_number"`
	ItemID         string         `json:"item_id"`
	OutputIndex    int            `json:"output_index"`
	ContentIndex   int            `json:"content_index"`
	Part           *OutputContent `json:"part"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/output_text/delta
type OutputTextDeltaEvent struct {
	Type           string `json:"type"` // response.output_text.delta
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Delta          string `json:"delta"`
	Logprobs       []any  `json:"logprobs"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/output_text/done
type OutputTextDoneEvent struct {
	Type           string `json:"type"` // response.output_text.done
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Text           string `json:"text"`
	Logprobs       []any  `json:"logprobs"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/function_call_arguments/delta
type FunctionCallArgumentsDeltaEvent struct {
	Type           string `json:"type"` // response.function_call_arguments.delta
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	Delta          string `json:"delta"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/function_call_arguments/done
type FunctionCallArgumentsDoneEvent struct {
	Type           string `json:"type"` // response.function_call_arguments.done
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	Arguments      string `json:"arguments"`
}

// FunctionCallOutputItem represents a function call output item
type FunctionCallOutputItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // function_call
	Status    string `json:"status"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
}

// FunctionCallOutputItemAddedEvent is emitted when a function call output item is added
type FunctionCallOutputItemAddedEvent struct {
	Type           string                  `json:"type"` // response.output_item.added
	SequenceNumber int                     `json:"sequence_number"`
	OutputIndex    int                     `json:"output_index"`
	Item           *FunctionCallOutputItem `json:"item"`
}

// FunctionCallOutputItemDoneEvent is emitted when a function call output item is done
type FunctionCallOutputItemDoneEvent struct {
	Type           string                  `json:"type"` // response.output_item.done
	SequenceNumber int                     `json:"sequence_number"`
	OutputIndex    int                     `json:"output_index"`
	Item           *FunctionCallOutputItem `json:"item"`
}

// CompactionOutputItem represents a compaction item in the output
type CompactionOutputItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"` // compaction
	Content          string `json:"content,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

// CompactionOutputItemAddedEvent is emitted when a compaction item is added
type CompactionOutputItemAddedEvent struct {
	Type           string                `json:"type"` // response.output_item.added
	SequenceNumber int                   `json:"sequence_number"`
	OutputIndex    int                   `json:"output_index"`
	Item           *CompactionOutputItem `json:"item"`
}

// CompactionOutputItemDoneEvent is emitted when a compaction item is done
type CompactionOutputItemDoneEvent struct {
	Type           string                `json:"type"` // response.output_item.done
	SequenceNumber int                   `json:"sequence_number"`
	OutputIndex    int                   `json:"output_index"`
	Item           *CompactionOutputItem `json:"item"`
}

// ReasoningOutputItem represents a reasoning item in the output
type ReasoningOutputItem struct {
	ID     string `json:"id"`
	Type   string `json:"type"`   // reasoning
	Status string `json:"status"` // in_progress, completed

	Summary []ReasoningOutputSummary     `json:"summary"`
	Content []ReasoningOutputContentPart `json:"content,omitempty"`

	EncryptedContent string `json:"encrypted_content,omitempty"`
}

// ReasoningOutputSummary represents a summary part in reasoning output
type ReasoningOutputSummary struct {
	Type string `json:"type"` // summary_text
	Text string `json:"text"`
}

// ReasoningOutputContentPart represents a content part in reasoning output
type ReasoningOutputContentPart struct {
	Type string `json:"type"` // reasoning_text
	Text string `json:"text"`
}

// ReasoningOutputItemAddedEvent is emitted when a reasoning item is added
type ReasoningOutputItemAddedEvent struct {
	Type           string               `json:"type"` // response.output_item.added
	SequenceNumber int                  `json:"sequence_number"`
	OutputIndex    int                  `json:"output_index"`
	Item           *ReasoningOutputItem `json:"item"`
}

// ReasoningOutputItemDoneEvent is emitted when a reasoning item is done
type ReasoningOutputItemDoneEvent struct {
	Type           string               `json:"type"` // response.output_item.done
	SequenceNumber int                  `json:"sequence_number"`
	OutputIndex    int                  `json:"output_index"`
	Item           *ReasoningOutputItem `json:"item"`
}

// ReasoningSummaryPartAddedEvent is emitted when a reasoning summary part is added
type ReasoningSummaryPartAddedEvent struct {
	Type           string                  `json:"type"` // response.reasoning_summary_part.added
	SequenceNumber int                     `json:"sequence_number"`
	ItemID         string                  `json:"item_id"`
	OutputIndex    int                     `json:"output_index"`
	SummaryIndex   int                     `json:"summary_index"`
	Part           *ReasoningOutputSummary `json:"part"`
}

// ReasoningSummaryPartDoneEvent is emitted when a reasoning summary part is done
type ReasoningSummaryPartDoneEvent struct {
	Type           string                  `json:"type"` // response.reasoning_summary_part.done
	SequenceNumber int                     `json:"sequence_number"`
	ItemID         string                  `json:"item_id"`
	OutputIndex    int                     `json:"output_index"`
	SummaryIndex   int                     `json:"summary_index"`
	Part           *ReasoningOutputSummary `json:"part"`
}

// ReasoningSummaryTextDeltaEvent is emitted when reasoning summary text delta is received
type ReasoningSummaryTextDeltaEvent struct {
	Type           string `json:"type"` // response.reasoning_summary_text.delta
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	SummaryIndex   int    `json:"summary_index"`
	Delta          string `json:"delta"`
}

// ReasoningSummaryTextDoneEvent is emitted when reasoning summary text is done
type ReasoningSummaryTextDoneEvent struct {
	Type           string `json:"type"` // response.reasoning_summary_text.done
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	SummaryIndex   int    `json:"summary_index"`
	Text           string `json:"text"`
}

// ReasoningContentPartAddedEvent is emitted when a reasoning content part is added
type ReasoningContentPartAddedEvent struct {
	Type           string                      `json:"type"` // response.content_part.added
	SequenceNumber int                         `json:"sequence_number"`
	ItemID         string                      `json:"item_id"`
	OutputIndex    int                         `json:"output_index"`
	ContentIndex   int                         `json:"content_index"`
	Part           *ReasoningOutputContentPart `json:"part"`
}

// ReasoningContentPartDoneEvent is emitted when a reasoning content part is done
type ReasoningContentPartDoneEvent struct {
	Type           string                      `json:"type"` // response.content_part.done
	SequenceNumber int                         `json:"sequence_number"`
	ItemID         string                      `json:"item_id"`
	OutputIndex    int                         `json:"output_index"`
	ContentIndex   int                         `json:"content_index"`
	Part           *ReasoningOutputContentPart `json:"part"`
}

// ReasoningTextDeltaEvent is emitted when reasoning text delta is received
type ReasoningTextDeltaEvent struct {
	Type           string `json:"type"` // response.reasoning_text.delta
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Delta          string `json:"delta"`
}

// ReasoningTextDoneEvent is emitted when reasoning text is done
type ReasoningTextDoneEvent struct {
	Type           string `json:"type"` // response.reasoning_text.done
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Text           string `json:"text"`
}

type RefusalContentPart struct {
	Type    string `json:"type"` // refusal
	Refusal string `json:"refusal"`
}

// RefusalContentPartAddedEvent is emitted when a refusal content part is added
type RefusalContentPartAddedEvent struct {
	Type           string              `json:"type"` // response.content_part.added
	SequenceNumber int                 `json:"sequence_number"`
	ItemID         string              `json:"item_id"`
	OutputIndex    int                 `json:"output_index"`
	ContentIndex   int                 `json:"content_index"`
	Part           *RefusalContentPart `json:"part"`
}

// RefusalContentPartDoneEvent is emitted when a refusal content part is done
type RefusalContentPartDoneEvent struct {
	Type           string              `json:"type"` // response.content_part.done
	SequenceNumber int                 `json:"sequence_number"`
	ItemID         string              `json:"item_id"`
	OutputIndex    int                 `json:"output_index"`
	ContentIndex   int                 `json:"content_index"`
	Part           *RefusalContentPart `json:"part"`
}

// RefusalDeltaEvent is emitted while refusal text streams in
type RefusalDeltaEvent struct {
	Type           string `json:"type"` // response.refusal.delta
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Delta          string `json:"delta"`
}

// RefusalDoneEvent is emitted when the refusal is complete
type RefusalDoneEvent struct {
	Type           string `json:"type"` // response.refusal.done
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Refusal        string `json:"refusal"`
}

// ResponseErrorEvent is a stream-level error event. Distinct from
// response.failed, which carries a full Response with status=failed.
// https://platform.openai.com/docs/api-reference/responses-streaming/response/error
type ResponseErrorEvent struct {
	Type           string `json:"type"` // response.error
	SequenceNumber int    `json:"sequence_number"`
	Code           string `json:"code,omitempty"`
	Message        string `json:"message"`
	Param          string `json:"param,omitempty"`
}

// CustomToolCallInputDeltaEvent is emitted while a custom tool's input streams
type CustomToolCallInputDeltaEvent struct {
	Type           string `json:"type"` // response.custom_tool_call_input.delta
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	Delta          string `json:"delta"`
}

// CustomToolCallInputDoneEvent is emitted when a custom tool's input completes
type CustomToolCallInputDoneEvent struct {
	Type           string `json:"type"` // response.custom_tool_call_input.done
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	Input          string `json:"input"`
}

// CustomToolCallOutputItemAddedEvent wraps custom_tool_call in output_item.added
type CustomToolCallOutputItemAddedEvent struct {
	Type           string              `json:"type"` // response.output_item.added
	SequenceNumber int                 `json:"sequence_number"`
	OutputIndex    int                 `json:"output_index"`
	Item           *CustomToolCallItem `json:"item"`
}

// CustomToolCallOutputItemDoneEvent wraps custom_tool_call in output_item.done
type CustomToolCallOutputItemDoneEvent struct {
	Type           string              `json:"type"` // response.output_item.done
	SequenceNumber int                 `json:"sequence_number"`
	OutputIndex    int                 `json:"output_index"`
	Item           *CustomToolCallItem `json:"item"`
}

// ApplyPatchCallOutputItemAddedEvent wraps apply_patch_call in output_item.added
type ApplyPatchCallOutputItemAddedEvent struct {
	Type           string              `json:"type"` // response.output_item.added
	SequenceNumber int                 `json:"sequence_number"`
	OutputIndex    int                 `json:"output_index"`
	Item           *ApplyPatchCallItem `json:"item"`
}

// ApplyPatchCallOutputItemDoneEvent wraps apply_patch_call in output_item.done
type ApplyPatchCallOutputItemDoneEvent struct {
	Type           string              `json:"type"` // response.output_item.done
	SequenceNumber int                 `json:"sequence_number"`
	OutputIndex    int                 `json:"output_index"`
	Item           *ApplyPatchCallItem `json:"item"`
}

// ComputerCallOutputItemAddedEvent wraps computer_call in output_item.added
// ShellCallItem represents a shell_call or local_shell_call in the output;
// the item type lives on the wrapping ResponseOutput.
type ShellCallItem struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	CallID string          `json:"call_id"`
	Status string          `json:"status"`
	Action json.RawMessage `json:"action,omitempty"`
}

type ShellCallOutputItemAddedEvent struct {
	Type           string         `json:"type"` // response.output_item.added
	SequenceNumber int            `json:"sequence_number"`
	OutputIndex    int            `json:"output_index"`
	Item           *ShellCallItem `json:"item"`
}

type ShellCallOutputItemDoneEvent struct {
	Type           string         `json:"type"` // response.output_item.done
	SequenceNumber int            `json:"sequence_number"`
	OutputIndex    int            `json:"output_index"`
	Item           *ShellCallItem `json:"item"`
}

type ComputerCallOutputItemAddedEvent struct {
	Type           string            `json:"type"` // response.output_item.added
	SequenceNumber int               `json:"sequence_number"`
	OutputIndex    int               `json:"output_index"`
	Item           *ComputerCallItem `json:"item"`
}

// ComputerCallOutputItemDoneEvent wraps computer_call in output_item.done
type ComputerCallOutputItemDoneEvent struct {
	Type           string            `json:"type"` // response.output_item.done
	SequenceNumber int               `json:"sequence_number"`
	OutputIndex    int               `json:"output_index"`
	Item           *ComputerCallItem `json:"item"`
}

// ToolSearchCallOutputItemAddedEvent wraps tool_search_call in output_item.added
type ToolSearchCallOutputItemAddedEvent struct {
	Type           string              `json:"type"` // response.output_item.added
	SequenceNumber int                 `json:"sequence_number"`
	OutputIndex    int                 `json:"output_index"`
	Item           *ToolSearchCallItem `json:"item"`
}

// ToolSearchCallOutputItemDoneEvent wraps tool_search_call in output_item.done
type ToolSearchCallOutputItemDoneEvent struct {
	Type           string              `json:"type"` // response.output_item.done
	SequenceNumber int                 `json:"sequence_number"`
	OutputIndex    int                 `json:"output_index"`
	Item           *ToolSearchCallItem `json:"item"`
}
