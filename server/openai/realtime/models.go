package realtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

type clientEvent struct {
	Type    string `json:"type"`
	EventID string `json:"event_id"`

	Session  json.RawMessage `json:"session"`
	Response json.RawMessage `json:"response"`
	Item     json.RawMessage `json:"item"`

	Audio string `json:"audio"`

	ItemID       string `json:"item_id"`
	ResponseID   string `json:"response_id"`
	ContentIndex int    `json:"content_index"`
	AudioEndMS   int    `json:"audio_end_ms"`
}

type conversationItem struct {
	ID     string `json:"id,omitempty"`
	Object string `json:"object,omitempty"`
	Type   string `json:"type"`
	Status string `json:"status,omitempty"`
	Role   string `json:"role,omitempty"`

	Content []itemContent `json:"content,omitempty"`

	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type itemContent struct {
	Type string `json:"type"`

	Text       string `json:"text,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	Audio      string `json:"audio,omitempty"`
}

func (item *conversationItem) normalize() {
	if item.ID == "" {
		item.ID = newID("item")
	}

	if item.Object == "" {
		item.Object = "realtime.item"
	}

	if item.Status == "" {
		item.Status = "completed"
	}
}

func (item conversationItem) message() (provider.Message, error) {
	var role provider.MessageRole
	switch strings.ToLower(item.Role) {
	case "system":
		role = provider.MessageRoleSystem
	case "user":
		role = provider.MessageRoleUser
	case "assistant":
		role = provider.MessageRoleAssistant
	default:
		return provider.Message{}, fmt.Errorf("unsupported conversation role %q", item.Role)
	}

	var content []provider.Content
	for _, part := range item.Content {
		switch part.Type {
		case "input_text", "text":
			content = append(content, provider.TextContent(part.Text))
		case "output_text":
			content = append(content, provider.TextContent(part.Text))
		case "input_audio":
			if part.Transcript != "" {
				content = append(content, provider.TextContent(part.Transcript))
			}
		}
	}

	return provider.Message{Role: role, Content: content}, nil
}

func (item conversationItem) audio() string {
	for _, content := range item.Content {
		if content.Type == "input_audio" && content.Audio != "" {
			return content.Audio
		}
	}

	return ""
}

type sessionConfig struct {
	Instructions string
	Voice        string

	InputAudio  provider.RealtimeAudioFormat
	OutputAudio provider.RealtimeAudioFormat

	MaxTokens   *int
	Temperature *float32
	TopP        *float32

	Tools      []provider.Tool
	ToolChoice provider.ToolChoice

	OutputModalities []string
	TurnDetection    *provider.RealtimeTurnDetection
	Transcription    *provider.RealtimeTranscription
	NoiseReduction   provider.RealtimeNoiseReduction
	Truncation       *provider.RealtimeTruncation
}

func newSessionConfig(defaults provider.RealtimeOptions) sessionConfig {
	modalities := modalityStrings(defaults.OutputModalities)
	if len(modalities) == 0 {
		modalities = []string{"audio"}
	}

	return sessionConfig{
		Instructions: defaults.Instructions,
		Voice:        defaults.Voice,

		InputAudio:  defaults.InputAudio,
		OutputAudio: defaults.OutputAudio,

		MaxTokens:   defaults.MaxTokens,
		Temperature: defaults.Temperature,
		TopP:        defaults.TopP,

		Tools:      slices.Clone(defaults.Tools),
		ToolChoice: defaults.ToolChoice,

		OutputModalities: modalities,
		TurnDetection:    cloneTurnDetection(defaults.TurnDetection),
		Transcription:    cloneTranscription(defaults.InputTranscription),
		NoiseReduction:   defaults.InputNoiseReduction,
		Truncation:       cloneTruncation(defaults.Truncation),
	}
}

func (c sessionConfig) options(history []provider.Message) provider.RealtimeOptions {
	return provider.RealtimeOptions{
		Instructions: c.Instructions,
		Voice:        c.Voice,

		InputAudio:  c.InputAudio,
		OutputAudio: c.OutputAudio,

		MaxTokens:   c.MaxTokens,
		Temperature: c.Temperature,
		TopP:        c.TopP,

		Tools:      slices.Clone(c.Tools),
		ToolChoice: c.ToolChoice,

		OutputModalities:    providerModalities(c.OutputModalities),
		TurnDetection:       cloneTurnDetection(c.TurnDetection),
		InputTranscription:  cloneTranscription(c.Transcription),
		InputNoiseReduction: c.NoiseReduction,
		OutputTranscription: containsModality(c.OutputModalities, "audio"),
		Truncation:          cloneTruncation(c.Truncation),

		History: slices.Clone(history),
	}
}

func (c *sessionConfig) apply(data json.RawMessage) error {
	if len(data) == 0 || string(data) == "null" {
		return errors.New("session is required")
	}

	updated := *c
	updated.Tools = slices.Clone(c.Tools)
	updated.OutputModalities = slices.Clone(c.OutputModalities)
	updated.TurnDetection = cloneTurnDetection(c.TurnDetection)
	updated.Transcription = cloneTranscription(c.Transcription)
	updated.Truncation = cloneTruncation(c.Truncation)

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("invalid session: %w", err)
	}

	if raw, ok := fields["instructions"]; ok {
		if err := json.Unmarshal(raw, &updated.Instructions); err != nil {
			return errors.New("session.instructions must be a string")
		}
	}

	if raw, ok := fields["voice"]; ok {
		voice, err := parseVoice(raw)
		if err != nil {
			return err
		}
		updated.Voice = voice
	}

	if raw, ok := fields["input_audio_format"]; ok {
		format, err := parseAudioFormat(raw, updated.InputAudio)
		if err != nil {
			return fmt.Errorf("session.input_audio_format: %w", err)
		}
		updated.InputAudio = format
	}

	if raw, ok := fields["output_audio_format"]; ok {
		format, err := parseAudioFormat(raw, updated.OutputAudio)
		if err != nil {
			return fmt.Errorf("session.output_audio_format: %w", err)
		}
		updated.OutputAudio = format
	}

	if raw, ok := fields["input_audio_transcription"]; ok {
		transcription, err := parseTranscription(raw)
		if err != nil {
			return fmt.Errorf("session.input_audio_transcription: %w", err)
		}
		updated.Transcription = transcription
	}

	if raw, ok := fields["turn_detection"]; ok {
		turnDetection, err := parseTurnDetection(raw)
		if err != nil {
			return fmt.Errorf("session.turn_detection: %w", err)
		}
		updated.TurnDetection = turnDetection
	}

	if raw, ok := fields["audio"]; ok {
		if err := applyAudioConfig(&updated, raw); err != nil {
			return err
		}
	}

	if raw, ok := fields["output_modalities"]; ok {
		if err := json.Unmarshal(raw, &updated.OutputModalities); err != nil {
			return errors.New("session.output_modalities must be an array")
		}
	}

	if raw, ok := fields["modalities"]; ok {
		if err := json.Unmarshal(raw, &updated.OutputModalities); err != nil {
			return errors.New("session.modalities must be an array")
		}
	}

	if raw, ok := fields["max_output_tokens"]; ok {
		maxTokens, err := parseMaxTokens(raw)
		if err != nil {
			return err
		}
		updated.MaxTokens = maxTokens
	}

	if raw, ok := fields["max_response_output_tokens"]; ok {
		maxTokens, err := parseMaxTokens(raw)
		if err != nil {
			return err
		}
		updated.MaxTokens = maxTokens
	}

	if raw, ok := fields["temperature"]; ok {
		var value float32
		if err := json.Unmarshal(raw, &value); err != nil {
			return errors.New("session.temperature must be a number")
		}
		updated.Temperature = &value
	}

	if raw, ok := fields["top_p"]; ok {
		var value float32
		if err := json.Unmarshal(raw, &value); err != nil {
			return errors.New("session.top_p must be a number")
		}
		updated.TopP = &value
	}

	if raw, ok := fields["tools"]; ok {
		tools, err := parseTools(raw)
		if err != nil {
			return err
		}
		updated.Tools = tools
	}

	if raw, ok := fields["tool_choice"]; ok {
		updated.ToolChoice = parseToolChoice(raw)
	}

	if raw, ok := fields["truncation"]; ok {
		truncation, err := parseTruncation(raw)
		if err != nil {
			return fmt.Errorf("session.truncation: %w", err)
		}
		updated.Truncation = truncation
	}

	if len(updated.OutputModalities) != 1 {
		return errors.New("session.output_modalities must contain exactly one modality")
	}

	for _, modality := range updated.OutputModalities {
		if modality != "audio" && modality != "text" {
			return fmt.Errorf("unsupported output modality %q", modality)
		}
	}

	if err := validateAudioFormat("input", updated.InputAudio); err != nil {
		return err
	}
	if err := validateAudioFormat("output", updated.OutputAudio); err != nil {
		return err
	}

	*c = updated
	return nil
}

func applyAudioConfig(config *sessionConfig, data json.RawMessage) error {
	var audio map[string]json.RawMessage
	if err := json.Unmarshal(data, &audio); err != nil {
		return errors.New("session.audio must be an object")
	}

	if raw, ok := audio["input"]; ok {
		var input map[string]json.RawMessage
		if err := json.Unmarshal(raw, &input); err != nil {
			return errors.New("session.audio.input must be an object")
		}

		if value, ok := input["format"]; ok {
			format, err := parseAudioFormat(value, config.InputAudio)
			if err != nil {
				return fmt.Errorf("session.audio.input.format: %w", err)
			}
			config.InputAudio = format
		}

		if value, ok := input["transcription"]; ok {
			transcription, err := parseTranscription(value)
			if err != nil {
				return fmt.Errorf("session.audio.input.transcription: %w", err)
			}
			config.Transcription = transcription
		}

		if value, ok := input["turn_detection"]; ok {
			turnDetection, err := parseTurnDetection(value)
			if err != nil {
				return fmt.Errorf("session.audio.input.turn_detection: %w", err)
			}
			config.TurnDetection = turnDetection
		}

		if value, ok := input["noise_reduction"]; ok {
			noiseReduction, err := parseNoiseReduction(value)
			if err != nil {
				return fmt.Errorf("session.audio.input.noise_reduction: %w", err)
			}
			config.NoiseReduction = noiseReduction
		}
	}

	if raw, ok := audio["output"]; ok {
		var output map[string]json.RawMessage
		if err := json.Unmarshal(raw, &output); err != nil {
			return errors.New("session.audio.output must be an object")
		}

		if value, ok := output["format"]; ok {
			format, err := parseAudioFormat(value, config.OutputAudio)
			if err != nil {
				return fmt.Errorf("session.audio.output.format: %w", err)
			}
			config.OutputAudio = format
		}

		if value, ok := output["voice"]; ok {
			voice, err := parseVoice(value)
			if err != nil {
				return err
			}
			config.Voice = voice
		}
	}

	return nil
}

func parseVoice(data json.RawMessage) (string, error) {
	var voice string
	if json.Unmarshal(data, &voice) == nil {
		return voice, nil
	}

	var custom struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &custom) == nil && custom.ID != "" {
		return custom.ID, nil
	}

	return "", errors.New("session audio voice must be a string")
}

func parseTranscription(data json.RawMessage) (*provider.RealtimeTranscription, error) {
	if string(data) == "null" {
		return nil, nil
	}
	var value struct {
		Model    string `json:"model"`
		Language string `json:"language"`
		Prompt   string `json:"prompt"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, errors.New("must be an object or null")
	}
	if value.Model == "" {
		value.Model = "gpt-live-transcribe"
	}
	return &provider.RealtimeTranscription{Model: value.Model, Language: value.Language, Prompt: value.Prompt}, nil
}

func parseTurnDetection(data json.RawMessage) (*provider.RealtimeTurnDetection, error) {
	if string(data) == "null" {
		return nil, nil
	}
	var value struct {
		Type              string   `json:"type"`
		Eagerness         string   `json:"eagerness"`
		Threshold         *float32 `json:"threshold"`
		PrefixPaddingMS   int64    `json:"prefix_padding_ms"`
		SilenceDurationMS int64    `json:"silence_duration_ms"`
		IdleTimeoutMS     int64    `json:"idle_timeout_ms"`
		CreateResponse    *bool    `json:"create_response"`
		InterruptResponse *bool    `json:"interrupt_response"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, errors.New("must be an object or null")
	}
	if value.Type == "" {
		value.Type = string(provider.RealtimeTurnDetectionServer)
	}
	if value.Type != string(provider.RealtimeTurnDetectionServer) && value.Type != string(provider.RealtimeTurnDetectionSemantic) {
		return nil, fmt.Errorf("unsupported type %q", value.Type)
	}
	result := &provider.RealtimeTurnDetection{
		Type:              provider.RealtimeTurnDetectionType(value.Type),
		Eagerness:         value.Eagerness,
		Threshold:         value.Threshold,
		PrefixPadding:     time.Duration(value.PrefixPaddingMS) * time.Millisecond,
		SilenceDuration:   time.Duration(value.SilenceDurationMS) * time.Millisecond,
		IdleTimeout:       time.Duration(value.IdleTimeoutMS) * time.Millisecond,
		CreateResponse:    true,
		InterruptResponse: true,
	}
	if value.CreateResponse != nil {
		result.CreateResponse = *value.CreateResponse
	}
	if value.InterruptResponse != nil {
		result.InterruptResponse = *value.InterruptResponse
	}
	return result, nil
}

func parseNoiseReduction(data json.RawMessage) (provider.RealtimeNoiseReduction, error) {
	if string(data) == "null" {
		return "", nil
	}
	var value struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return "", errors.New("must be an object or null")
	}
	switch provider.RealtimeNoiseReduction(value.Type) {
	case provider.RealtimeNoiseReductionNear, provider.RealtimeNoiseReductionFar:
		return provider.RealtimeNoiseReduction(value.Type), nil
	default:
		return "", fmt.Errorf("unsupported type %q", value.Type)
	}
}

func parseTruncation(data json.RawMessage) (*provider.RealtimeTruncation, error) {
	var mode string
	if json.Unmarshal(data, &mode) == nil {
		if mode != "disabled" {
			return nil, fmt.Errorf("unsupported mode %q", mode)
		}
		return &provider.RealtimeTruncation{Disabled: true}, nil
	}
	var value struct {
		Type           string   `json:"type"`
		RetentionRatio *float32 `json:"retention_ratio"`
		TokenLimits    struct {
			PostInstructions *int `json:"post_instructions"`
		} `json:"token_limits"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, errors.New("must be an object or \"disabled\"")
	}
	if value.Type != "retention_ratio" {
		return nil, fmt.Errorf("unsupported type %q", value.Type)
	}
	return &provider.RealtimeTruncation{
		RetentionRatio: value.RetentionRatio, PostInstructionTokens: value.TokenLimits.PostInstructions,
	}, nil
}

func parseAudioFormat(data json.RawMessage, current provider.RealtimeAudioFormat) (provider.RealtimeAudioFormat, error) {
	var legacy string
	if json.Unmarshal(data, &legacy) == nil {
		switch legacy {
		case "pcm16", "audio/pcm":
			current.Encoding = provider.RealtimeAudioPCM
		case "g711_ulaw", "audio/pcmu":
			current.Encoding = provider.RealtimeAudioPCMU
		case "g711_alaw", "audio/pcma":
			current.Encoding = provider.RealtimeAudioPCMA
		default:
			return current, fmt.Errorf("unsupported format %q", legacy)
		}

		return current, nil
	}

	var object struct {
		Type string `json:"type"`
		Rate int    `json:"rate"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return current, errors.New("must be a format string or object")
	}

	switch object.Type {
	case "audio/pcm":
		current.Encoding = provider.RealtimeAudioPCM
	case "audio/pcmu":
		current.Encoding = provider.RealtimeAudioPCMU
	case "audio/pcma":
		current.Encoding = provider.RealtimeAudioPCMA
	default:
		return current, fmt.Errorf("unsupported format %q", object.Type)
	}

	if object.Rate != 0 {
		current.SampleRate = object.Rate
	}

	return current, nil
}

func validateAudioFormat(name string, format provider.RealtimeAudioFormat) error {
	if format.SampleSize != 16 || format.Channels != 1 {
		return fmt.Errorf("session %s audio must be mono 16-bit", name)
	}
	switch format.Encoding {
	case provider.RealtimeAudioPCM:
		if format.SampleRate <= 0 {
			return fmt.Errorf("session %s PCM audio requires a sample rate", name)
		}
	case provider.RealtimeAudioPCMU, provider.RealtimeAudioPCMA:
		if format.SampleRate != 8000 {
			return fmt.Errorf("session %s G.711 audio must use 8000 Hz", name)
		}
	default:
		return fmt.Errorf("session %s audio encoding %q is unsupported", name, format.Encoding)
	}

	return nil
}

func parseMaxTokens(data json.RawMessage) (*int, error) {
	var value int
	if json.Unmarshal(data, &value) == nil {
		if value <= 0 {
			return nil, errors.New("session max output tokens must be positive")
		}
		return &value, nil
	}

	var unlimited string
	if json.Unmarshal(data, &unlimited) == nil && unlimited == "inf" {
		return nil, nil
	}

	return nil, errors.New("session max output tokens must be a positive integer or \"inf\"")
}

func parseTools(data json.RawMessage) ([]provider.Tool, error) {
	var values []struct {
		Type        string         `json:"type"`
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, errors.New("session.tools must be an array")
	}

	tools := make([]provider.Tool, 0, len(values))
	for _, value := range values {
		if value.Type != "" && value.Type != "function" {
			return nil, fmt.Errorf("realtime tool type %q is unsupported", value.Type)
		}
		if value.Name == "" {
			return nil, errors.New("realtime function tool name is required")
		}
		if value.Parameters == nil {
			value.Parameters = map[string]any{"type": "object"}
		}

		tools = append(tools, provider.Tool{
			Name:        value.Name,
			Description: value.Description,
			Parameters:  value.Parameters,
		})
	}

	return tools, nil
}

func parseToolChoice(data json.RawMessage) provider.ToolChoice {
	var choice string
	if json.Unmarshal(data, &choice) != nil {
		return provider.ToolChoiceAny
	}

	switch choice {
	case "none":
		return provider.ToolChoiceNone
	case "required", "any":
		return provider.ToolChoiceAny
	default:
		return provider.ToolChoiceAuto
	}
}

func modalityStrings(values []provider.RealtimeModality) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = string(value)
	}
	return result
}

func providerModalities(values []string) []provider.RealtimeModality {
	result := make([]provider.RealtimeModality, len(values))
	for i, value := range values {
		result[i] = provider.RealtimeModality(value)
	}
	return result
}

func cloneTurnDetection(value *provider.RealtimeTurnDetection) *provider.RealtimeTurnDetection {
	if value == nil {
		return nil
	}
	result := *value
	if value.Threshold != nil {
		threshold := *value.Threshold
		result.Threshold = &threshold
	}
	return &result
}

func cloneTranscription(value *provider.RealtimeTranscription) *provider.RealtimeTranscription {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneTruncation(value *provider.RealtimeTruncation) *provider.RealtimeTruncation {
	if value == nil {
		return nil
	}
	result := *value
	if value.RetentionRatio != nil {
		ratio := *value.RetentionRatio
		result.RetentionRatio = &ratio
	}
	if value.PostInstructionTokens != nil {
		tokens := *value.PostInstructionTokens
		result.PostInstructionTokens = &tokens
	}
	return &result
}

func transcriptionObject(value *provider.RealtimeTranscription) any {
	if value == nil {
		return nil
	}
	result := map[string]any{"model": value.Model}
	if value.Language != "" {
		result["language"] = value.Language
	}
	if value.Prompt != "" {
		result["prompt"] = value.Prompt
	}
	return result
}

func noiseReductionObject(value provider.RealtimeNoiseReduction) any {
	if value == "" {
		return nil
	}
	return map[string]any{"type": string(value)}
}

func turnDetectionObject(value *provider.RealtimeTurnDetection) any {
	if value == nil {
		return nil
	}
	detectionType := value.Type
	if detectionType == "" {
		detectionType = provider.RealtimeTurnDetectionServer
	}
	result := map[string]any{
		"type": string(detectionType), "create_response": value.CreateResponse,
		"interrupt_response": value.InterruptResponse,
	}
	if value.Eagerness != "" {
		result["eagerness"] = value.Eagerness
	}
	if value.Threshold != nil {
		result["threshold"] = *value.Threshold
	}
	if value.PrefixPadding > 0 {
		result["prefix_padding_ms"] = value.PrefixPadding.Milliseconds()
	}
	if value.SilenceDuration > 0 {
		result["silence_duration_ms"] = value.SilenceDuration.Milliseconds()
	}
	if value.IdleTimeout > 0 {
		result["idle_timeout_ms"] = value.IdleTimeout.Milliseconds()
	}
	return result
}

func truncationObject(value *provider.RealtimeTruncation) any {
	if value == nil {
		return nil
	}
	if value.Disabled {
		return "disabled"
	}
	if value.RetentionRatio == nil {
		return nil
	}
	result := map[string]any{"type": "retention_ratio", "retention_ratio": *value.RetentionRatio}
	if value.PostInstructionTokens != nil {
		result["token_limits"] = map[string]any{"post_instructions": *value.PostInstructionTokens}
	}
	return result
}

func toolChoiceObject(value provider.ToolChoice) string {
	if value == provider.ToolChoiceAny {
		return "required"
	}
	if value == provider.ToolChoiceNone {
		return "none"
	}
	return "auto"
}

func (c sessionConfig) object(id, model string) map[string]any {
	maxTokens := any("inf")
	if c.MaxTokens != nil {
		maxTokens = *c.MaxTokens
	}

	tools := make([]map[string]any, 0, len(c.Tools))
	for _, tool := range c.Tools {
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
		})
	}

	result := map[string]any{
		"id":                id,
		"object":            "realtime.session",
		"type":              "realtime",
		"model":             model,
		"output_modalities": c.OutputModalities,
		"instructions":      c.Instructions,
		"max_output_tokens": maxTokens,
		"tools":             tools,
		"tool_choice":       toolChoiceObject(c.ToolChoice),
		"audio": map[string]any{
			"input": map[string]any{
				"format":          audioFormatObject(c.InputAudio),
				"transcription":   transcriptionObject(c.Transcription),
				"noise_reduction": noiseReductionObject(c.NoiseReduction),
				"turn_detection":  turnDetectionObject(c.TurnDetection),
			},
			"output": map[string]any{
				"format": audioFormatObject(c.OutputAudio),
				"voice":  c.Voice,
			},
		},
	}
	if truncation := truncationObject(c.Truncation); truncation != nil {
		result["truncation"] = truncation
	}
	return result
}

func audioFormatObject(format provider.RealtimeAudioFormat) map[string]any {
	typeName := "audio/pcm"
	switch format.Encoding {
	case provider.RealtimeAudioPCMU:
		typeName = "audio/pcmu"
	case provider.RealtimeAudioPCMA:
		typeName = "audio/pcma"
	}

	object := map[string]any{"type": typeName}
	if format.Encoding == provider.RealtimeAudioPCM {
		object["rate"] = format.SampleRate
	}

	return object
}

func newID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
