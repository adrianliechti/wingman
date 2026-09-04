package realtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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

	ItemID         string `json:"item_id"`
	PreviousItemID string `json:"previous_item_id"`
	ResponseID     string `json:"response_id"`
	ContentIndex   int    `json:"content_index"`
	AudioEndMS     int    `json:"audio_end_ms"`
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
		default:
			return provider.Message{}, fmt.Errorf("unsupported conversation content type %q", part.Type)
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
	if err := validateObjectFields("session", fields,
		"type", "instructions", "voice", "input_audio_format", "output_audio_format",
		"input_audio_transcription", "turn_detection", "audio", "output_modalities", "modalities",
		"max_output_tokens", "max_response_output_tokens", "temperature", "top_p", "tools",
		"tool_choice", "truncation", "tracing",
	); err != nil {
		return err
	}
	if raw, ok := fields["tracing"]; ok && strings.TrimSpace(string(raw)) != "null" {
		return errors.New("session.tracing must be null because no-store mode is enforced")
	}
	if raw, ok := fields["type"]; ok {
		var sessionType string
		if err := json.Unmarshal(raw, &sessionType); err != nil || sessionType != "realtime" {
			return errors.New("session.type must be \"realtime\"")
		}
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
		toolChoice, err := parseToolChoice(raw)
		if err != nil {
			return err
		}
		updated.ToolChoice = toolChoice
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
	if audio == nil {
		return errors.New("session.audio must be an object")
	}
	if err := validateObjectFields("session.audio", audio, "input", "output"); err != nil {
		return err
	}

	if raw, ok := audio["input"]; ok {
		var input map[string]json.RawMessage
		if err := json.Unmarshal(raw, &input); err != nil {
			return errors.New("session.audio.input must be an object")
		}
		if input == nil {
			return errors.New("session.audio.input must be an object")
		}
		if err := validateObjectFields("session.audio.input", input, "format", "transcription", "turn_detection", "noise_reduction"); err != nil {
			return err
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
		if output == nil {
			return errors.New("session.audio.output must be an object")
		}
		if err := validateObjectFields("session.audio.output", output, "format", "voice"); err != nil {
			return err
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, errors.New("must be an object or null")
	}
	if err := validateObjectFields("session audio transcription", fields, "model", "language", "prompt"); err != nil {
		return nil, err
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, errors.New("must be an object or null")
	}
	if err := validateObjectFields("turn detection", fields,
		"type", "eagerness", "threshold", "prefix_padding_ms", "silence_duration_ms",
		"idle_timeout_ms", "create_response", "interrupt_response",
	); err != nil {
		return nil, err
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
	if value.Threshold != nil && (*value.Threshold < 0 || *value.Threshold > 1) {
		return nil, errors.New("threshold must be between 0 and 1")
	}
	if value.PrefixPaddingMS < 0 || value.SilenceDurationMS < 0 || value.IdleTimeoutMS < 0 {
		return nil, errors.New("turn detection durations must not be negative")
	}
	if value.IdleTimeoutMS > 0 && (value.IdleTimeoutMS < 5000 || value.IdleTimeoutMS > 30000) {
		return nil, errors.New("idle_timeout_ms must be between 5000 and 30000")
	}
	if value.Type == string(provider.RealtimeTurnDetectionServer) && value.Eagerness != "" {
		return nil, errors.New("eagerness is only supported for semantic_vad")
	}
	if value.Type == string(provider.RealtimeTurnDetectionSemantic) {
		if value.Threshold != nil || value.PrefixPaddingMS != 0 || value.SilenceDurationMS != 0 || value.IdleTimeoutMS != 0 {
			return nil, errors.New("threshold and duration fields are only supported for server_vad")
		}
		switch value.Eagerness {
		case "", "auto", "low", "medium", "high":
		default:
			return nil, fmt.Errorf("unsupported semantic_vad eagerness %q", value.Eagerness)
		}
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return "", errors.New("must be an object or null")
	}
	if err := validateObjectFields("noise reduction", fields, "type"); err != nil {
		return "", err
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
		switch mode {
		case "auto":
			return nil, nil
		case "disabled":
			return &provider.RealtimeTruncation{Disabled: true}, nil
		default:
			return nil, fmt.Errorf("unsupported mode %q", mode)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, errors.New("must be an object or \"auto\" or \"disabled\"")
	}
	if err := validateObjectFields("truncation", fields, "type", "retention_ratio", "token_limits"); err != nil {
		return nil, err
	}
	if raw, ok := fields["token_limits"]; ok {
		var tokenLimits map[string]json.RawMessage
		if err := json.Unmarshal(raw, &tokenLimits); err != nil {
			return nil, errors.New("truncation.token_limits must be an object")
		}
		if err := validateObjectFields("truncation.token_limits", tokenLimits, "post_instructions"); err != nil {
			return nil, err
		}
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
	if value.RetentionRatio == nil || *value.RetentionRatio < 0 || *value.RetentionRatio > 1 {
		return nil, errors.New("retention_ratio must be between 0 and 1")
	}
	if value.TokenLimits.PostInstructions != nil && *value.TokenLimits.PostInstructions < 0 {
		return nil, errors.New("token_limits.post_instructions must not be negative")
	}
	return &provider.RealtimeTruncation{
		RetentionRatio: value.RetentionRatio, PostInstructionTokens: value.TokenLimits.PostInstructions,
	}, nil
}

func parseMetadata(data json.RawMessage) (map[string]string, error) {
	if string(data) == "null" {
		return nil, nil
	}
	var metadata map[string]string
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, errors.New("response.metadata must be an object with string values or null")
	}
	if len(metadata) > 16 {
		return nil, errors.New("response.metadata must contain at most 16 entries")
	}
	for key, value := range metadata {
		if utf8.RuneCountInString(key) > 64 {
			return nil, fmt.Errorf("response.metadata key %q exceeds 64 characters", key)
		}
		if utf8.RuneCountInString(value) > 512 {
			return nil, fmt.Errorf("response.metadata value for %q exceeds 512 characters", key)
		}
	}
	return metadata, nil
}

func validateObjectFields(context string, fields map[string]json.RawMessage, allowed ...string) error {
	for name := range fields {
		if !slices.Contains(allowed, name) {
			return fmt.Errorf("%s field %q is not supported", context, name)
		}
	}
	return nil
}

func parseAudioFormat(data json.RawMessage, current provider.RealtimeAudioFormat) (provider.RealtimeAudioFormat, error) {
	var legacy string
	if json.Unmarshal(data, &legacy) == nil {
		switch legacy {
		case "pcm16", "audio/pcm":
			return openAIAudioFormat(provider.RealtimeAudioPCM), nil
		case "g711_ulaw", "audio/pcmu":
			return openAIAudioFormat(provider.RealtimeAudioPCMU), nil
		case "g711_alaw", "audio/pcma":
			return openAIAudioFormat(provider.RealtimeAudioPCMA), nil
		default:
			return current, fmt.Errorf("unsupported format %q", legacy)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return current, errors.New("must be a format string or object")
	}
	if err := validateObjectFields("audio format", fields, "type", "rate"); err != nil {
		return current, err
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
		current = openAIAudioFormat(provider.RealtimeAudioPCM)
	case "audio/pcmu":
		current = openAIAudioFormat(provider.RealtimeAudioPCMU)
	case "audio/pcma":
		current = openAIAudioFormat(provider.RealtimeAudioPCMA)
	default:
		return current, fmt.Errorf("unsupported format %q", object.Type)
	}

	if object.Rate != 0 {
		current.SampleRate = object.Rate
	}

	return current, nil
}

func openAIAudioFormat(encoding provider.RealtimeAudioEncoding) provider.RealtimeAudioFormat {
	format := provider.RealtimeAudioFormat{
		Encoding: encoding,
		Channels: 1,
	}
	if encoding == provider.RealtimeAudioPCM {
		format.SampleRate = 24000
		format.SampleSize = 16
	} else {
		format.SampleRate = 8000
		format.SampleSize = 8
	}
	return format
}

func validateAudioFormat(name string, format provider.RealtimeAudioFormat) error {
	if format.Channels != 1 {
		return fmt.Errorf("session %s audio must be mono", name)
	}
	switch format.Encoding {
	case provider.RealtimeAudioPCM:
		if format.SampleRate != 24000 || format.SampleSize != 16 {
			return fmt.Errorf("session %s PCM audio must be 16-bit at 24000 Hz", name)
		}
	case provider.RealtimeAudioPCMU, provider.RealtimeAudioPCMA:
		if format.SampleRate != 8000 || format.SampleSize != 8 {
			return fmt.Errorf("session %s G.711 audio must be 8-bit at 8000 Hz", name)
		}
	default:
		return fmt.Errorf("session %s audio encoding %q is unsupported", name, format.Encoding)
	}

	return nil
}

func parseMaxTokens(data json.RawMessage) (*int, error) {
	var value int
	if json.Unmarshal(data, &value) == nil {
		if value < 1 || value > 4096 {
			return nil, errors.New("session max output tokens must be between 1 and 4096")
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
	var fields []map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, errors.New("session.tools must be an array")
	}
	if fields == nil {
		return nil, errors.New("session.tools must be an array")
	}
	for _, tool := range fields {
		if err := validateObjectFields("realtime tool", tool, "type", "name", "description", "parameters"); err != nil {
			return nil, err
		}
	}
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

func parseToolChoice(data json.RawMessage) (provider.ToolChoice, error) {
	var choice string
	if json.Unmarshal(data, &choice) != nil {
		return "", errors.New("session.tool_choice must be a string; named function choices are not supported")
	}

	switch choice {
	case "none":
		return provider.ToolChoiceNone, nil
	case "required", "any":
		return provider.ToolChoiceAny, nil
	case "auto":
		return provider.ToolChoiceAuto, nil
	}

	return "", fmt.Errorf("session.tool_choice %q is unsupported", choice)
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
		"tracing":           nil,
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
