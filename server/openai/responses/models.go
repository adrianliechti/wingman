package responses

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/openai/shared"
)

// https://platform.openai.com/docs/api-reference/responses/create
type ResponsesRequest struct {
	Model string `json:"model,omitzero"`
	Tier  string `json:"service_tier,omitzero"`

	Stream bool `json:"stream,omitzero"`

	Instructions string         `json:"instructions,omitzero"`
	Input        ResponsesInput `json:"input"`

	MaxOutputTokens *int             `json:"max_output_tokens,omitzero"`
	Reasoning       *ReasoningConfig `json:"reasoning,omitzero"`
	Text            *TextConfig      `json:"text,omitzero"`

	Tools             []Tool      `json:"tools,omitempty"`
	ToolChoice        *ToolChoice `json:"tool_choice,omitzero"`
	ParallelToolCalls *bool       `json:"parallel_tool_calls,omitzero"`

	ContextManagement []ContextManagementConfig `json:"context_management,omitempty"`
	Truncation        string                    `json:"truncation,omitzero"`

	Include []string `json:"include,omitempty"`

	PromptCacheKey       *string             `json:"prompt_cache_key,omitzero"`
	PromptCacheRetention *string             `json:"prompt_cache_retention,omitzero"`
	PromptCacheOptions   *PromptCacheOptions `json:"prompt_cache_options,omitzero"`
}

type PromptCacheOptions = shared.PromptCacheOptions

// PromptCacheBreakpoint marks the end of a reusable prefix on a content part.
type PromptCacheBreakpoint struct {
	Mode string `json:"mode,omitzero"`
	TTL  string `json:"ttl,omitzero"`
}

// ContextManagementConfig represents a context management entry
type ContextManagementConfig struct {
	Type string `json:"type"`

	CompactThreshold *int64 `json:"compact_threshold,omitzero"`
}

// ReasoningConfig contains configuration options for reasoning models
type ReasoningConfig struct {
	Effort  *ReasoningEffort `json:"effort"`
	Summary *any             `json:"summary"`
	Context *string          `json:"context,omitzero"`
}

// ResponseReasoning includes effective metadata, which can be unavailable
// on other backends even when the request supplied a preference.
type ResponseReasoning struct {
	Effort  *ReasoningEffort `json:"effort"`
	Summary *any             `json:"summary"`
	Context *string          `json:"context"`
	Mode    string           `json:"mode"`
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
	Format    *TextFormat `json:"format,omitzero"`
	Verbosity Verbosity   `json:"verbosity,omitzero"`
}

type Verbosity string

var (
	VerbosityLow    Verbosity = "low"
	VerbosityMedium Verbosity = "medium"
	VerbosityHigh   Verbosity = "high"
)

// TextFormat represents the format configuration for text output
type TextFormat struct {
	Type string `json:"type,omitzero"` // "text", "json_object", or "json_schema"

	// For json_schema type
	Name        string         `json:"name,omitzero"`
	Description string         `json:"description,omitzero"`
	Schema      map[string]any `json:"schema,omitempty"`
	Strict      *bool          `json:"strict,omitzero"`
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
	ToolTypeWebSearch  ToolType = "web_search"
)

// Tool represents a tool in the request
type Tool struct {
	Type ToolType `json:"type"`

	// For function tools
	Name        string `json:"name,omitzero"`
	Description string `json:"description,omitzero"`

	Strict     *bool          `json:"strict,omitzero"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Async      *bool          `json:"async,omitzero"`

	// For namespace tools (groups of function/custom tools).
	Tools []Tool `json:"tools,omitempty"`

	// For custom tools (like apply_patch)
	Format *CustomToolFormat `json:"format,omitzero"`

	// For tool_search tools: "server" or "client".
	Execution string `json:"execution,omitzero"`

	// For function/custom tools paired with a tool_search tool: hide from the
	// initial tool list, surface via tool_search instead.
	DeferLoading *bool `json:"defer_loading,omitzero"`

	// For computer tool
	DisplayWidth  int    `json:"display_width,omitzero"`
	DisplayHeight int    `json:"display_height,omitzero"`
	Environment   string `json:"environment,omitzero"` // "browser", "ubuntu", "windows", "mac"
}

type ToolChoice struct {
	Mode ToolChoiceMode `json:"mode,omitzero"`

	AllowedTools []ToolChoiceAllowedTool `json:"allowed_tools,omitempty"`

	// Hosted captures typed tool_choice objects for hosted tools, e.g.
	// {"type":"file_search"} or {"type":"mcp","server_label":"x","name":"y"}.
	// When set, Mode is also Auto so dispatch falls back to the auto path.
	Hosted *HostedToolChoice `json:"-"`

	// raw preserves the client's original JSON so the response echo is
	// byte-faithful regardless of how the value was normalized internally.
	raw jsontext.Value
}

type ToolChoiceMode string

const (
	ToolChoiceModeNone     ToolChoiceMode = "none"
	ToolChoiceModeAuto     ToolChoiceMode = "auto"
	ToolChoiceModeRequired ToolChoiceMode = "required"
)

type ToolChoiceAllowedTool struct {
	Type string `json:"type"`
	Name string `json:"name,omitzero"`
}

// HostedToolChoice is the wire shape for typed hosted-tool choices like
// {"type":"file_search"} or {"type":"mcp","server_label":"x","name":"y"}.
type HostedToolChoice struct {
	Type        string `json:"type"`
	ServerLabel string `json:"server_label,omitzero"`
	Name        string `json:"name,omitzero"`
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

// MarshalJSONTo echoes the exact value the client sent when available; a
// ToolChoice built in code falls back to the official wire shapes.
func (t ToolChoice) MarshalJSONTo(enc *jsontext.Encoder) error {
	if len(t.raw) > 0 {
		return enc.WriteValue(t.raw)
	}

	if t.Hosted != nil {
		return json.MarshalEncode(enc, t.Hosted)
	}

	if len(t.AllowedTools) == 0 {
		return json.MarshalEncode(enc, string(t.Mode))
	}

	if t.Mode == ToolChoiceModeRequired && len(t.AllowedTools) == 1 && t.AllowedTools[0].Name != "" {
		return json.MarshalEncode(enc, map[string]string{
			"type": "function",
			"name": t.AllowedTools[0].Name,
		})
	}

	return json.MarshalEncode(enc, map[string]any{
		"type":  "allowed_tools",
		"mode":  t.Mode,
		"tools": t.AllowedTools,
	})
}

func (t *ToolChoice) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	// This union must echo the original value, including provider-specific fields.
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	value := ToolChoice{raw: raw.Clone()}
	if raw.Kind() == '"' || raw.Kind() == 'n' {
		if err := json.Unmarshal(raw, &value.Mode, dec.Options()); err != nil {
			return err
		}
	} else {
		var wire struct {
			Type         string                  `json:"type"`
			Name         string                  `json:"name"`
			ServerLabel  string                  `json:"server_label"`
			Mode         ToolChoiceMode          `json:"mode"`
			Tools        []ToolChoiceAllowedTool `json:"tools"`
			AllowedTools []ToolChoiceAllowedTool `json:"allowed_tools"`
		}
		if err := json.Unmarshal(raw, &wire, dec.Options()); err != nil {
			return err
		}
		switch {
		case wire.Type == "function" && wire.Name != "":
			value.Mode = ToolChoiceModeRequired
			value.AllowedTools = []ToolChoiceAllowedTool{{Type: wire.Type, Name: wire.Name}}
		case wire.Type == "allowed_tools":
			value.Mode, value.AllowedTools = wire.Mode, wire.Tools
			if value.Mode == "" {
				value.Mode = ToolChoiceModeAuto
			}
		default:
			if _, ok := hostedToolChoiceTypes[wire.Type]; ok {
				value.Mode = ToolChoiceModeAuto
				value.Hosted = &HostedToolChoice{Type: wire.Type, Name: wire.Name, ServerLabel: wire.ServerLabel}
			} else {
				value.Mode, value.AllowedTools = wire.Mode, wire.AllowedTools
			}
		}
	}
	*t = value
	return nil
}

// CustomToolFormat describes the format for custom tools
type CustomToolFormat struct {
	Type       string `json:"type,omitzero"`       // "grammar"
	Syntax     string `json:"syntax,omitzero"`     // "lark"
	Definition string `json:"definition,omitzero"` // the grammar definition
}

// InputItemType represents the type of input item
type InputItemType string

const (
	InputItemTypeMessage              InputItemType = "message"
	InputItemTypeAdditionalTools      InputItemType = "additional_tools"
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
	InputItemTypeConfigurationUpdate  InputItemType = "configuration_update"
	InputItemTypeCompactionTrigger    InputItemType = "compaction_trigger"
)

type ResponsesInput struct {
	Items []InputItem `json:"-"`
}

// InputItem represents a single item in the input array
type InputItem struct {
	Type InputItemType `json:"type,omitzero"`

	// For message type
	*InputMessage

	// For additional_tools type
	*InputAdditionalTools

	// For reasoning type
	*InputReasoning

	// For compaction type
	*InputCompaction

	// For configuration_update type
	*InputConfigurationUpdate

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

// InputAdditionalTools makes tools available from this point in the input.
type InputAdditionalTools struct {
	ID    string      `json:"id,omitzero"`
	Role  MessageRole `json:"role"`
	Tools []Tool      `json:"tools"`
}

// InputComputerCall represents a computer call in the input (for multi-turn)
type InputComputerCall struct {
	ID      string `json:"id,omitzero"`
	CallID  string `json:"call_id,omitzero"`
	Status  string `json:"status,omitzero"`
	Actions []any  `json:"actions,omitempty"`

	PendingSafetyChecks []SafetyCheck `json:"pending_safety_checks,omitempty"`
}

// InputComputerCallOutput represents the result of a computer call
type InputComputerCallOutput struct {
	CallID string `json:"call_id,omitzero"`
	Output any    `json:"output,omitzero"`
	Status string `json:"status,omitzero"`

	AcknowledgedSafetyChecks []SafetyCheck `json:"acknowledged_safety_checks,omitempty"`
}

type SafetyCheck struct {
	ID      string `json:"id,omitzero"`
	Code    string `json:"code,omitzero"`
	Message string `json:"message,omitzero"`
}

// InputShellCall represents a shell_call or local_shell_call in the input
// (for multi-turn). The action carries either {commands, timeout_ms,
// max_output_length} (shell) or {command, env, timeout_ms, user,
// working_directory} (local_shell).
type InputShellCall struct {
	ID     string         `json:"id,omitzero"`
	CallID string         `json:"call_id,omitzero"`
	Status string         `json:"status,omitzero"`
	Action jsontext.Value `json:"action,omitzero"`
}

// InputShellCallOutput represents a shell_call_output or
// local_shell_call_output. Output is a content list for shell and a plain
// JSON string for local_shell.
type InputShellCallOutput struct {
	ID     string         `json:"id,omitzero"`
	CallID string         `json:"call_id,omitzero"`
	Status string         `json:"status,omitzero"`
	Output jsontext.Value `json:"output,omitzero"`
}

// InputApplyPatchCall represents an apply_patch call in the input (for multi-turn)
type InputApplyPatchCall struct {
	ID        string              `json:"id,omitzero"`
	CallID    string              `json:"call_id,omitzero"`
	Status    string              `json:"status,omitzero"`
	Operation ApplyPatchOperation `json:"operation"`
}

// InputApplyPatchCallOutput represents the result of an apply_patch call
type InputApplyPatchCallOutput struct {
	CallID string         `json:"call_id,omitzero"`
	Output []InputContent `json:"output,omitempty"`
	Status string         `json:"status,omitzero"`
}

func (o *InputApplyPatchCallOutput) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		CallID string      `json:"call_id"`
		Status string      `json:"status"`
		Output inputOutput `json:"output"`
	}
	if err := json.UnmarshalDecode(dec, &wire); err != nil {
		return err
	}
	o.CallID = wire.CallID
	o.Status = wire.Status
	o.Output = wire.Output
	return nil
}

// InputToolSearchCall represents a prior tool_search call (codex echoes
// these back on multi-turn). Arguments is raw JSON because the model is free
// to choose its own argument shape.
type InputToolSearchCall struct {
	ID        string         `json:"id,omitzero"`
	CallID    string         `json:"call_id,omitzero"`
	Status    string         `json:"status,omitzero"`
	Execution string         `json:"execution,omitzero"`
	Arguments jsontext.Value `json:"arguments,omitzero"`
}

// InputToolSearchOutput represents the tools returned by the client (or the
// server, for hosted tool_search) for a prior tool_search_call. Tools is the
// raw JSON array of tool definitions so it passes through unmodified.
type InputToolSearchOutput struct {
	ID        string         `json:"id,omitzero"`
	CallID    string         `json:"call_id,omitzero"`
	Status    string         `json:"status,omitzero"`
	Execution string         `json:"execution,omitzero"`
	Tools     jsontext.Value `json:"tools,omitzero"`
}

// InputCustomToolCall represents a custom (freeform grammar) tool call in the
// input. Codex registers apply_patch as a custom tool, so its prior calls come
// back as custom_tool_call items whose Input is the raw patch envelope.
type InputCustomToolCall struct {
	ID        string `json:"id,omitzero"`
	CallID    string `json:"call_id,omitzero"`
	Name      string `json:"name,omitzero"`
	Namespace string `json:"namespace,omitzero"`
	Input     string `json:"input,omitzero"`
	Status    string `json:"status,omitzero"`
	Async     bool   `json:"async,omitzero"`
}

// InputCustomToolCallOutput represents the result of a custom tool call.
type InputCustomToolCallOutput struct {
	CallID string         `json:"call_id,omitzero"`
	Name   string         `json:"name,omitzero"`
	Output []InputContent `json:"output,omitempty"`
	Status string         `json:"status,omitzero"`
}

func (o *InputCustomToolCallOutput) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		CallID string      `json:"call_id"`
		Name   string      `json:"name"`
		Status string      `json:"status"`
		Output inputOutput `json:"output"`
	}
	if err := json.UnmarshalDecode(dec, &wire); err != nil {
		return err
	}
	o.CallID = wire.CallID
	o.Name = wire.Name
	o.Status = wire.Status
	o.Output = wire.Output
	return nil
}

// InputReasoning represents a reasoning item in the input
type InputReasoning struct {
	ID               string                 `json:"id,omitzero"`
	Summary          []ReasoningSummaryPart `json:"summary,omitempty"`
	Content          jsontext.Value         `json:"content,omitzero"`           // Can be null
	EncryptedContent string                 `json:"encrypted_content,omitzero"` // Base64 encoded
}

// InputCompaction represents a compaction item in the input
type InputCompaction struct {
	ID               string `json:"id,omitzero"`
	Content          string `json:"content,omitzero"`
	EncryptedContent string `json:"encrypted_content,omitzero"`
}

// InputConfigurationUpdate changes reasoning effort at this exact point in a
// GPT-6 Astra conversation.
type InputConfigurationUpdate struct {
	Reasoning ConfigurationUpdateReasoning `json:"reasoning"`
}

type ConfigurationUpdateReasoning struct {
	Effort ReasoningEffort `json:"effort"`
}

// ReasoningSummaryPart represents a part of the reasoning summary
type ReasoningSummaryPart struct {
	Type string `json:"type,omitzero"` // "summary_text"
	Text string `json:"text,omitzero"`
}

// InputFunctionCall represents a function call in the input
type InputFunctionCall struct {
	ID        string `json:"id,omitzero"`
	CallID    string `json:"call_id,omitzero"`
	Name      string `json:"name,omitzero"`
	Namespace string `json:"namespace,omitzero"`
	Arguments string `json:"arguments,omitzero"`
	Status    string `json:"status,omitzero"` // "in_progress", "completed"
	Async     bool   `json:"async,omitzero"`
}

// InputFunctionCallOutput represents a function call output in the input
type InputFunctionCallOutput struct {
	CallID string         `json:"call_id,omitzero"`
	Output []InputContent `json:"output,omitempty"`
}

func (o *InputFunctionCallOutput) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		CallID string      `json:"call_id"`
		Output inputOutput `json:"output"`
	}
	if err := json.UnmarshalDecode(dec, &wire); err != nil {
		return err
	}
	o.CallID = wire.CallID
	o.Output = wire.Output
	return nil
}

// unmarshalInputOutput accepts the polymorphic `output` field defined by
// the OpenAI Responses API: either a plain string, or an array of content
// parts. A string is normalized to a single output_text part.
type inputOutput []InputContent

func (out *inputOutput) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case '"':
		var text string
		if err := json.UnmarshalDecode(dec, &text); err != nil {
			return err
		}
		*out = []InputContent{{Type: OutputContentText, Text: text}}
		return nil
	case '[', 'n':
		return json.UnmarshalDecode(dec, (*[]InputContent)(out))
	default:
		return errors.New("output must be a string or array of content parts")
	}
}

func (ri *ResponsesInput) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case '"':
		var text string
		if err := json.UnmarshalDecode(dec, &text); err != nil {
			return err
		}
		ri.Items = []InputItem{{Type: InputItemTypeMessage, InputMessage: &InputMessage{Role: MessageRoleUser, Content: []InputContent{{Type: InputContentText, Text: text}}}}}
		return nil
	case '[', 'n':
		return json.UnmarshalDecode(dec, &ri.Items, jsonutil.PreserveNumbers)
	default:
		return errors.New("input must be a string or an array of items")
	}
}

func (item *InputItem) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	// Buffer one item so the discriminator may appear anywhere in the object.
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	if raw.Kind() != '{' {
		return errors.New("input items must be objects")
	}
	var kind struct {
		Type InputItemType `json:"type"`
	}
	if err := json.Unmarshal(raw, &kind); err != nil {
		return err
	}
	value := InputItem{Type: kind.Type}
	var target any
	switch kind.Type {
	case InputItemTypeMessage, "":
		value.Type = InputItemTypeMessage
		target = &value.InputMessage
	case InputItemTypeAdditionalTools:
		target = &value.InputAdditionalTools
	case InputItemTypeReasoning:
		target = &value.InputReasoning
	case InputItemTypeCompaction:
		target = &value.InputCompaction
	case InputItemTypeConfigurationUpdate:
		target = &value.InputConfigurationUpdate
	case InputItemTypeFunctionCall:
		target = &value.InputFunctionCall
	case InputItemTypeFunctionCallOutput:
		target = &value.InputFunctionCallOutput
	case InputItemTypeApplyPatchCall:
		target = &value.InputApplyPatchCall
	case InputItemTypeApplyPatchCallOutput:
		target = &value.InputApplyPatchCallOutput
	case InputItemTypeCustomToolCall:
		target = &value.InputCustomToolCall
	case InputItemTypeCustomToolCallOutput:
		target = &value.InputCustomToolCallOutput
	case InputItemTypeComputerCall:
		target = &value.InputComputerCall
	case InputItemTypeComputerCallOutput:
		target = &value.InputComputerCallOutput
	case InputItemTypeShellCall, InputItemTypeLocalShellCall:
		target = &value.InputShellCall
	case InputItemTypeShellCallOutput, InputItemTypeLocalShellCallOutput:
		target = &value.InputShellCallOutput
	case InputItemTypeToolSearchCall:
		target = &value.InputToolSearchCall
	case InputItemTypeToolSearchOutput:
		target = &value.InputToolSearchOutput
	case InputItemTypeCompactionTrigger:
		// Bare marker item; carries no payload.
	default:
		return fmt.Errorf("unknown input item type: %s", kind.Type)
	}
	if target != nil {
		if err := json.Unmarshal(raw, target, dec.Options(), jsonutil.PreserveNumbers); err != nil {
			return err
		}
	}

	*item = value
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
	Role MessageRole `json:"role,omitzero"`
	// Phase is valid on assistant history messages and must be replayed.
	Phase string `json:"phase,omitzero"`

	Content []InputContent `json:"content,omitempty"`
}

func (im *InputMessage) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	if dec.PeekKind() == '"' {
		var text string
		if err := json.UnmarshalDecode(dec, &text); err != nil {
			return err
		}
		*im = InputMessage{Role: MessageRoleUser, Content: []InputContent{{Type: InputContentText, Text: text}}}
		return nil
	}
	var wire struct {
		Role    MessageRole    `json:"role"`
		Phase   string         `json:"phase"`
		Text    string         `json:"text"`
		Content jsontext.Value `json:"content"`
	}
	if err := json.UnmarshalDecode(dec, &wire); err != nil {
		return err
	}
	value := InputMessage{Role: wire.Role, Phase: wire.Phase}
	if wire.Role != "" && wire.Text != "" {
		value.Content = []InputContent{{Type: InputContentText, Text: wire.Text}}
	} else {
		switch wire.Content.Kind() {
		case '"':
			var text string
			if err := json.Unmarshal(wire.Content, &text, dec.Options()); err != nil {
				return err
			}
			value.Content = []InputContent{{Type: InputContentText, Text: text}}
		case '[':
			if err := json.Unmarshal(wire.Content, &value.Content, dec.Options()); err != nil {
				return err
			}
		default:
			return errors.New("message content must be a string or array of content parts")
		}
	}
	*im = value
	return nil
}

type InputContent struct {
	Type InputContentType `json:"type,omitzero"`
	Text string           `json:"text,omitzero"`

	Refusal string `json:"refusal,omitzero"`

	ImageURL string                `json:"image_url,omitzero"`
	Detail   *provider.ImageDetail `json:"detail,omitzero"`

	Filename string `json:"filename,omitzero"`
	FileURL  string `json:"file_url,omitzero"`
	FileData string `json:"file_data,omitzero"`

	PromptCacheBreakpoint *PromptCacheBreakpoint `json:"prompt_cache_breakpoint,omitzero"`
}

type InputContentType string

const (
	InputContentText    InputContentType = "input_text"
	InputContentImage   InputContentType = "input_image"
	InputContentFile    InputContentType = "input_file"
	InputContentRefusal InputContentType = "refusal"

	OutputContentText InputContentType = "output_text"
)

type Response struct {
	ID     string `json:"id"`
	Object string `json:"object"` // response
	Model  string `json:"model"`

	Tier           *string        `json:"service_tier"`
	AccessPrograms jsontext.Value `json:"access_programs"`

	Status            string             `json:"status"` // completed, failed, in_progress, incomplete
	CreatedAt         int64              `json:"created_at"`
	CompletedAt       *int64             `json:"completed_at"`
	Error             *ResponseError     `json:"error"`
	IncompleteDetails *IncompleteDetails `json:"incomplete_details"`

	Background         bool    `json:"background"`
	Store              bool    `json:"store"`
	PreviousResponseID *string `json:"previous_response_id"`

	Instructions    *string            `json:"instructions"`
	MaxOutputTokens *int               `json:"max_output_tokens"`
	Reasoning       *ResponseReasoning `json:"reasoning"`
	Text            *TextConfig        `json:"text"`

	Tools             []any `json:"tools"`
	ToolChoice        any   `json:"tool_choice"`
	ParallelToolCalls bool  `json:"parallel_tool_calls"`
	MaxToolCalls      *int  `json:"max_tool_calls"`

	Output []ResponseOutput `json:"output"`

	FrequencyPenalty float32 `json:"frequency_penalty"`
	PresencePenalty  float32 `json:"presence_penalty"`
	TopLogprobs      int     `json:"top_logprobs"`
	Truncation       string  `json:"truncation"`

	PromptCacheKey       *string             `json:"prompt_cache_key"`
	PromptCacheRetention *string             `json:"prompt_cache_retention"`
	PromptCacheOptions   *PromptCacheOptions `json:"prompt_cache_options,omitzero"`

	Metadata         any     `json:"metadata"`
	User             *any    `json:"user"`
	SafetyIdentifier *string `json:"safety_identifier"`
	Moderation       *any    `json:"moderation"`

	Usage     *Usage           `json:"usage"`
	ToolUsage ToolUsage        `json:"tool_usage"`
	Billing   *ResponseBilling `json:"billing,omitzero"`
}

// These hosted tools are not executed by the Responses gateway, so their
// usage remains zero. Ordinary function calls are counted in Usage instead.
type ToolUsage struct {
	ImageGen struct {
		InputTokens         int               `json:"input_tokens"`
		InputTokensDetails  ImageTokenDetails `json:"input_tokens_details"`
		OutputTokens        int               `json:"output_tokens"`
		OutputTokensDetails ImageTokenDetails `json:"output_tokens_details"`
		TotalTokens         int               `json:"total_tokens"`
	} `json:"image_gen"`
	WebSearch struct {
		NumRequests int `json:"num_requests"`
	} `json:"web_search"`
}

type ImageTokenDetails struct {
	ImageTokens int `json:"image_tokens"`
	TextTokens  int `json:"text_tokens"`
}

// ResponseBilling represents billing information
type ResponseBilling struct {
	Payer string `json:"payer,omitzero"` // "developer"
}

// Usage contains token usage information
type Usage struct {
	InputTokens        int                 `json:"input_tokens"`
	InputTokensDetails *InputTokensDetails `json:"input_tokens_details"`

	OutputTokens        int                  `json:"output_tokens"`
	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details,omitzero"`

	TotalTokens int `json:"total_tokens"`
}

// InputTokensDetails contains detailed input token breakdown
type InputTokensDetails struct {
	CachedTokens            int                             `json:"cached_tokens"`
	CacheWriteTokens        int                             `json:"cache_write_tokens"`
	CacheWriteTokensDetails *shared.CacheWriteTokensDetails `json:"cache_write_tokens_details,omitzero"`
}

// OutputTokensDetails contains detailed output token breakdown
type OutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// ResponseError contains error details when a response fails
type ResponseError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Headers map[string]string `json:"headers,omitempty"`
}

// IncompleteDetails explains why a response stopped short
type IncompleteDetails struct {
	Reason string `json:"reason"` // max_output_tokens, content_filter
}

type ResponseOutput struct {
	Type ResponseOutputType `json:"type,omitzero"`

	*OutputMessage
	*FunctionCallOutputItem
	*ApplyPatchCallItem
	*CustomToolCallItem
	*ComputerCallItem
	*ShellCallItem
	*ToolSearchCallItem
	ToolSearchOutputItem *InputToolSearchOutput
	*ReasoningOutputItem
	*CompactionOutputItem
}

// UnmarshalJSONFrom selects exactly one output variant, independently of where
// its discriminator appears in the JSON object.
func (r *ResponseOutput) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	if raw.Kind() == 'n' {
		*r = ResponseOutput{}
		return nil
	}
	if raw.Kind() != '{' {
		return errors.New("response output items must be objects")
	}
	var kind struct {
		Type ResponseOutputType `json:"type"`
	}
	if err := json.Unmarshal(raw, &kind); err != nil {
		return err
	}
	value := ResponseOutput{Type: kind.Type}
	var target any
	switch kind.Type {
	case ResponseOutputTypeMessage:
		target = &value.OutputMessage
	case ResponseOutputTypeFunctionCall:
		target = &value.FunctionCallOutputItem
	case ResponseOutputTypeApplyPatchCall:
		target = &value.ApplyPatchCallItem
	case ResponseOutputTypeCustomToolCall:
		target = &value.CustomToolCallItem
	case ResponseOutputTypeComputerCall:
		target = &value.ComputerCallItem
	case ResponseOutputTypeShellCall, ResponseOutputTypeLocalShellCall:
		target = &value.ShellCallItem
	case ResponseOutputTypeToolSearchCall:
		target = &value.ToolSearchCallItem
	case ResponseOutputTypeToolSearchOutput:
		target = &value.ToolSearchOutputItem
	case ResponseOutputTypeReasoning:
		target = &value.ReasoningOutputItem
	case ResponseOutputTypeCompaction:
		target = &value.CompactionOutputItem
	default:
		return fmt.Errorf("unknown response output item type: %s", kind.Type)
	}
	if err := json.Unmarshal(raw, target, dec.Options(), jsonutil.PreserveNumbers); err != nil {
		return err
	}
	*r = value
	return nil
}

// MarshalJSONTo encodes only the selected variant. Normalize copies so the
// discriminator and defaults on the wire do not mutate the caller's item.
func (r ResponseOutput) MarshalJSONTo(enc *jsontext.Encoder) error {
	switch r.Type {
	case ResponseOutputTypeToolSearchOutput:
		if r.ToolSearchOutputItem == nil {
			return errors.New("tool-search output requires a payload")
		}
		return json.MarshalEncode(enc, struct {
			Type ResponseOutputType `json:"type"`
			*InputToolSearchOutput
			CallID *string `json:"call_id"`
		}{r.Type, r.ToolSearchOutputItem, toolSearchCallID(r.ToolSearchOutputItem.CallID, r.ToolSearchOutputItem.Execution)})
	case ResponseOutputTypeMessage:
		if r.OutputMessage != nil {
			return json.MarshalEncode(enc, struct {
				Type ResponseOutputType `json:"type"`
				*OutputMessage
				Phase *string `json:"phase"`
			}{r.Type, r.OutputMessage, outputPhase(r.OutputMessage.Phase)})
		}
	case ResponseOutputTypeFunctionCall:
		if r.FunctionCallOutputItem != nil {
			item := *r.FunctionCallOutputItem
			item.Type = string(r.Type)
			return json.MarshalEncode(enc, item)
		}
	case ResponseOutputTypeShellCall, ResponseOutputTypeLocalShellCall:
		if r.ShellCallItem != nil {
			item := *r.ShellCallItem
			item.Type = string(r.Type)
			if len(item.Action) == 0 {
				item.Action = emptyShellAction(r.Type)
			}
			if len(item.Environment) == 0 && r.Type == ResponseOutputTypeShellCall {
				item.Environment = shellLocalEnvironment
			}
			return json.MarshalEncode(enc, item)
		}
	case ResponseOutputTypeComputerCall:
		if r.ComputerCallItem != nil {
			item := *r.ComputerCallItem
			item.Type = string(r.Type)
			return json.MarshalEncode(enc, item)
		}
	case ResponseOutputTypeApplyPatchCall:
		if r.ApplyPatchCallItem != nil {
			item := *r.ApplyPatchCallItem
			item.Type = string(r.Type)
			return json.MarshalEncode(enc, item)
		}
	case ResponseOutputTypeToolSearchCall:
		if r.ToolSearchCallItem != nil {
			return json.MarshalEncode(enc, r.ToolSearchCallItem)
		}
	case ResponseOutputTypeCustomToolCall:
		if r.CustomToolCallItem != nil {
			item := *r.CustomToolCallItem
			item.Type = string(r.Type)
			return json.MarshalEncode(enc, item)
		}
	case ResponseOutputTypeReasoning:
		if r.ReasoningOutputItem != nil {
			item := *r.ReasoningOutputItem
			item.Type = string(r.Type)
			return json.MarshalEncode(enc, item)
		}
	case ResponseOutputTypeCompaction:
		if r.CompactionOutputItem != nil {
			item := *r.CompactionOutputItem
			item.Type = string(r.Type)
			return json.MarshalEncode(enc, item)
		}
	}
	// Fallback: just marshal the type.
	return json.MarshalEncode(enc, struct {
		Type ResponseOutputType `json:"type,omitzero"`
	}{Type: r.Type})
}

type ResponseOutputType string

var (
	ResponseOutputTypeMessage          ResponseOutputType = "message"
	ResponseOutputTypeFunctionCall     ResponseOutputType = "function_call"
	ResponseOutputTypeApplyPatchCall   ResponseOutputType = "apply_patch_call"
	ResponseOutputTypeCustomToolCall   ResponseOutputType = "custom_tool_call"
	ResponseOutputTypeComputerCall     ResponseOutputType = "computer_call"
	ResponseOutputTypeShellCall        ResponseOutputType = "shell_call"
	ResponseOutputTypeLocalShellCall   ResponseOutputType = "local_shell_call"
	ResponseOutputTypeToolSearchCall   ResponseOutputType = "tool_search_call"
	ResponseOutputTypeToolSearchOutput ResponseOutputType = "tool_search_output"
	ResponseOutputTypeReasoning        ResponseOutputType = "reasoning"
	ResponseOutputTypeCompaction       ResponseOutputType = "compaction"
)

// ToolSearchCallItem represents a tool_search call in the output.
type ToolSearchCallItem struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"` // tool_search_call
	CallID    string         `json:"call_id"`
	Status    string         `json:"status"`
	Execution string         `json:"execution"`
	Arguments jsontext.Value `json:"arguments"`
}

func toolSearchCallID(id, execution string) *string {
	if execution == "client" {
		return &id
	}
	return nil
}

func (item ToolSearchCallItem) MarshalJSONTo(enc *jsontext.Encoder) error {
	type call ToolSearchCallItem
	item.Type = "tool_search_call"
	if item.Execution == "" {
		item.Execution = "server"
	}
	if len(item.Arguments) == 0 {
		item.Arguments = jsontext.Value("{}")
	}
	return json.MarshalEncode(enc, struct {
		call
		CallID *string `json:"call_id"`
	}{call(item), toolSearchCallID(item.CallID, item.Execution)})
}

// ComputerCallItem represents a computer use tool call in the output
type ComputerCallItem struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // computer_call
	CallID  string `json:"call_id"`
	Status  string `json:"status"`
	Actions []any  `json:"actions"`

	PendingSafetyChecks []SafetyCheck `json:"pending_safety_checks"`
}

// ApplyPatchCallItem represents an apply_patch tool call in the output
type ApplyPatchCallItem struct {
	ID        string               `json:"id"`
	Type      string               `json:"type"` // apply_patch_call
	CallID    string               `json:"call_id"`
	Status    string               `json:"status"`
	Operation *ApplyPatchOperation `json:"operation,omitzero"`
}

type ApplyPatchOperation struct {
	Type string `json:"type"` // "create_file", "update_file", "delete_file"
	Path string `json:"path"`
	Diff string `json:"diff,omitzero"` // absent for delete_file
}

// CustomToolCallItem represents a custom (freeform grammar) tool call in the
// output. Used for Codex's apply_patch, where Input is the raw patch envelope.
type CustomToolCallItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // custom_tool_call
	CallID    string `json:"call_id"`
	Status    string `json:"status"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitzero"`
	Input     string `json:"input"`
	Async     bool   `json:"async,omitzero"`
}

type OutputMessage struct {
	ID string `json:"id,omitzero"`

	Role MessageRole `json:"role,omitzero"`

	Status string `json:"status,omitzero"` // completed

	Contents []OutputContent `json:"content"`

	Phase string `json:"phase,omitzero"` // final_answer
}

type OutputContent struct {
	Type        string `json:"type,omitzero"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
	Logprobs    []any  `json:"logprobs"`
}

func (c *OutputContent) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	type plain OutputContent
	var wire struct {
		plain
		Refusal string `json:"refusal"`
	}
	if err := json.UnmarshalDecode(dec, &wire, jsonutil.PreserveNumbers); err != nil {
		return err
	}
	value := OutputContent(wire.plain)
	if value.Type == "refusal" {
		value.Text = wire.Refusal
	}
	*c = value
	return nil
}

func (c OutputContent) MarshalJSONTo(enc *jsontext.Encoder) error {
	if c.Type == "refusal" {
		return json.MarshalEncode(enc, struct {
			Type    string `json:"type"`
			Refusal string `json:"refusal"`
		}{
			Type:    "refusal",
			Refusal: c.Text,
		})
	}

	type alias OutputContent
	return json.MarshalEncode(enc, alias(c))
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
	Phase   *string         `json:"phase"`
	Role    MessageRole     `json:"role,omitzero"`
}

func outputPhase(phase string) *string {
	if phase == "" {
		return nil
	}
	return &phase
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
	Name           string `json:"name"`
	Arguments      string `json:"arguments"`
}

// FunctionCallOutputItem represents a function call output item
type FunctionCallOutputItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // function_call
	Status    string `json:"status"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitzero"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
	Async     bool   `json:"async,omitzero"`
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
	Content          string `json:"content,omitzero"`
	EncryptedContent string `json:"encrypted_content"`
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
	Status string `json:"status"` // in_progress, completed, incomplete

	// Summary and Content are always arrays, as on OpenAI's items; the
	// terminal snapshot and the streamed item must serialize identically.
	Summary []ReasoningOutputSummary     `json:"summary"`
	Content []ReasoningOutputContentPart `json:"content"`

	EncryptedContent string `json:"encrypted_content,omitzero"`
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
	Status         string                  `json:"status,omitzero"` // incomplete when interrupted
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
// https://platform.openai.com/docs/api-reference/responses-streaming/error
type ResponseErrorEvent struct {
	Type           string  `json:"type"` // error
	SequenceNumber int     `json:"sequence_number"`
	Code           *string `json:"code"`
	Message        string  `json:"message"`
	Param          *string `json:"param"`
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
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	CallID      string         `json:"call_id"`
	Status      string         `json:"status"`
	Action      jsontext.Value `json:"action"`
	Environment jsontext.Value `json:"environment,omitzero"`
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
