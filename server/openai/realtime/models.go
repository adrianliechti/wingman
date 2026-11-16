package realtime

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

type clientEvent struct {
	Type    string `json:"type"`
	EventID string `json:"event_id"`

	Session  jsontext.Value `json:"session"`
	Response jsontext.Value `json:"response"`
	Item     jsontext.Value `json:"item"`

	Audio string `json:"audio"`

	ItemID         string `json:"item_id"`
	PreviousItemID string `json:"previous_item_id"`
	ResponseID     string `json:"response_id"`
	ContentIndex   int    `json:"content_index"`
	AudioEndMS     int    `json:"audio_end_ms"`
}

type conversationItem struct {
	ID     string `json:"id,omitzero"`
	Object string `json:"object,omitzero"`
	Type   string `json:"type"`
	Status string `json:"status,omitzero"`
	Role   string `json:"role,omitzero"`

	Content []itemContent `json:"content,omitempty"`

	CallID    string `json:"call_id,omitzero"`
	Name      string `json:"name,omitzero"`
	Arguments string `json:"arguments,omitzero"`
	Output    string `json:"output,omitzero"`
}

type itemContent struct {
	Type string `json:"type"`

	Text       string `json:"text,omitzero"`
	Transcript string `json:"transcript,omitzero"`
	Audio      string `json:"audio,omitzero"`
}

func (item *conversationItem) normalize() {
	if item.ID == "" {
		// Generated ids are forwarded to the provider; OpenAI caps item ids at
		// 32 characters, so keep "item_" plus 27 hex characters.
		item.ID = newID("item")[:32]
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

	return provider.Message{ID: item.ID, Role: role, Content: content}, nil
}

func (item conversationItem) audio() string {
	for _, content := range item.Content {
		if content.Type == "input_audio" && content.Audio != "" {
			return content.Audio
		}
	}

	return ""
}

const (
	sessionTypeRealtime      = "realtime"
	sessionTypeTranscription = "transcription"
	sessionTypeTranslation   = "translation"
)

type sessionConfig struct {
	Type string

	Instructions string
	Voice        string
	Language     string

	InputAudio  provider.RealtimeAudioFormat
	OutputAudio provider.RealtimeAudioFormat

	MaxTokens *int

	Tools      []provider.Tool
	ToolChoice provider.ToolChoice

	OutputModalities []string
	Transcription    *provider.RealtimeTranscription
	NoiseReduction   provider.RealtimeNoiseReduction
	Truncation       *provider.RealtimeTruncation

	// TurnDetection is nil when detection is off. Until the client sets it,
	// it only reports the provider's default, which the provider applies.
	TurnDetection    *provider.RealtimeTurnDetection
	turnDetectionSet bool
}

func newSessionConfig(defaults provider.RealtimeOptions) sessionConfig {
	modalities := modalityStrings(defaults.Output.Modalities)
	if len(modalities) == 0 {
		modalities = []string{"audio"}
	}

	return sessionConfig{
		Type: sessionTypeRealtime,

		Instructions: defaults.Instructions,
		Voice:        defaults.Output.Voice,

		InputAudio:  defaults.Input.Audio,
		OutputAudio: defaults.Output.Audio,

		MaxTokens: defaults.MaxTokens,

		Tools:      slices.Clone(defaults.Tools),
		ToolChoice: defaults.ToolChoice,

		OutputModalities: modalities,
		TurnDetection:    declaredTurnDetection(defaults.Input.TurnDetection),
		Transcription:    cloneTranscription(defaults.Input.Transcription),
		NoiseReduction:   defaults.Input.NoiseReduction,
		Truncation:       cloneTruncation(defaults.Truncation),
	}
}

func newTranscriptionConfig() sessionConfig {
	return sessionConfig{
		Type: sessionTypeTranscription,

		InputAudio: openAIAudioFormat(provider.RealtimeAudioPCM),

		TurnDetection: declaredTurnDetection(nil),
	}
}

func newTranslationConfig(defaults provider.RealtimeOptions) sessionConfig {
	return sessionConfig{
		Type: sessionTypeTranslation,

		Language:   defaults.Output.Language,
		InputAudio: openAIAudioFormat(provider.RealtimeAudioPCM),
	}
}

func (c *sessionConfig) applyTranslation(data jsontext.Value) error {
	var session struct {
		Audio struct {
			Input struct {
				Transcription  jsontext.Value `json:"transcription"`
				NoiseReduction jsontext.Value `json:"noise_reduction"`
			} `json:"input"`
			Output struct {
				Language *string `json:"language"`
			} `json:"output"`
		} `json:"audio"`
	}
	if err := json.Unmarshal(data, &session, json.RejectUnknownMembers(true)); err != nil {
		var semantic *json.SemanticError
		if errors.As(err, &semantic) && errors.Is(err, json.ErrUnknownName) {
			return fmt.Errorf("session field %q is not supported", semantic.JSONPointer)
		}
		return fmt.Errorf("invalid session: %w", err)
	}

	updated := *c

	if raw := session.Audio.Input.Transcription; raw.Kind() == 'n' {
		updated.Transcription = nil
	} else if len(raw) > 0 {
		var transcription struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(raw, &transcription, json.RejectUnknownMembers(true)); err != nil || transcription.Model == "" {
			return errors.New("session.audio.input.transcription must be null or an object with only a model")
		}
		updated.Transcription = &provider.RealtimeTranscription{Model: transcription.Model}
	}

	if raw := session.Audio.Input.NoiseReduction; len(raw) > 0 {
		noiseReduction, err := parseNoiseReduction(raw)
		if err != nil {
			return fmt.Errorf("session.audio.input.noise_reduction: %w", err)
		}
		updated.NoiseReduction = noiseReduction
	}

	if language := session.Audio.Output.Language; language != nil {
		updated.Language = *language
	}

	*c = updated
	return nil
}

func (c sessionConfig) options(history []provider.Message) provider.RealtimeOptions {
	// Every session type streams the same kind of input.
	input := provider.RealtimeInputOptions{
		Audio: c.InputAudio,

		TurnDetection:  c.providerTurnDetection(),
		Transcription:  cloneTranscription(c.Transcription),
		NoiseReduction: c.NoiseReduction,
	}

	switch c.Type {
	case sessionTypeTranslation:
		return provider.RealtimeOptions{
			Input:  input,
			Output: provider.RealtimeOutputOptions{Language: c.Language},
		}

	case sessionTypeTranscription:
		return provider.RealtimeOptions{
			Input: input,
			Output: provider.RealtimeOutputOptions{
				Modalities: []provider.RealtimeModality{provider.RealtimeModalityText},
			},
		}
	}

	return provider.RealtimeOptions{
		Instructions: c.Instructions,

		MaxTokens: c.MaxTokens,

		Tools:      slices.Clone(c.Tools),
		ToolChoice: c.ToolChoice,

		Truncation: cloneTruncation(c.Truncation),

		Input: input,
		Output: provider.RealtimeOutputOptions{
			Modalities: providerModalities(c.OutputModalities),

			Audio: c.OutputAudio,
			Voice: c.Voice,

			Transcription: containsModality(c.OutputModalities, "audio"),
		},

		History: slices.Clone(history),
	}
}

func (c *sessionConfig) apply(data jsontext.Value) error {
	if len(data) == 0 || data.Kind() == 'n' {
		return errors.New("session is required")
	}
	if c.Type == sessionTypeTranslation {
		return c.applyTranslation(data)
	}

	updated := *c
	updated.Tools = slices.Clone(c.Tools)
	updated.OutputModalities = slices.Clone(c.OutputModalities)
	updated.TurnDetection = cloneTurnDetection(c.TurnDetection)
	updated.Transcription = cloneTranscription(c.Transcription)
	updated.Truncation = cloneTruncation(c.Truncation)

	var fields map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("invalid session: %w", err)
	}
	if raw, ok := fields["type"]; ok {
		var sessionType string
		if err := json.Unmarshal(raw, &sessionType); err != nil || (sessionType != sessionTypeRealtime && sessionType != sessionTypeTranscription) {
			return errors.New("session.type must be \"realtime\" or \"transcription\"")
		}
		if sessionType != updated.Type {
			return fmt.Errorf("passing a %s session update to a %s session is not allowed", sessionType, updated.Type)
		}
	}

	allowed := []string{
		"type", "instructions", "voice", "input_audio_format", "output_audio_format",
		"input_audio_transcription", "turn_detection", "audio", "output_modalities", "modalities",
		"max_output_tokens", "max_response_output_tokens", "tools",
		"tool_choice", "truncation", "tracing",
	}
	if updated.Type == sessionTypeTranscription {
		allowed = []string{"type", "audio"}
	}
	if err := validateObjectFields("session", fields, allowed...); err != nil {
		return err
	}
	if raw, ok := fields["tracing"]; ok && raw.Kind() != 'n' {
		return errors.New("session.tracing must be null because no-store mode is enforced")
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
		updated.turnDetectionSet = true
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

	if raw, ok := fields["tools"]; ok {
		tools, err := parseTools(raw)
		if err != nil {
			return err
		}
		updated.Tools = tools
	}

	if raw, ok := fields["tool_choice"]; ok {
		toolOptions, err := parseToolChoice(raw)
		if err != nil {
			return err
		}
		// Session options cannot name a tool, so a named choice requires any call.
		updated.ToolChoice = toolOptions.Choice
	}

	if raw, ok := fields["truncation"]; ok {
		truncation, err := parseTruncation(raw)
		if err != nil {
			return fmt.Errorf("session.truncation: %w", err)
		}
		updated.Truncation = truncation
	}

	if err := validateAudioFormat("input", updated.InputAudio); err != nil {
		return err
	}

	if updated.Type == sessionTypeRealtime {
		if len(updated.OutputModalities) != 1 {
			return errors.New("session.output_modalities must contain exactly one modality")
		}

		for _, modality := range updated.OutputModalities {
			if modality != "audio" && modality != "text" {
				return fmt.Errorf("unsupported output modality %q", modality)
			}
		}

		if err := validateAudioFormat("output", updated.OutputAudio); err != nil {
			return err
		}
	}

	*c = updated
	return nil
}

func applyAudioConfig(config *sessionConfig, data jsontext.Value) error {
	var audio map[string]jsontext.Value
	if err := json.Unmarshal(data, &audio); err != nil {
		return errors.New("session.audio must be an object")
	}
	if audio == nil {
		return errors.New("session.audio must be an object")
	}
	allowed := []string{"input", "output"}
	if config.Type == sessionTypeTranscription {
		allowed = []string{"input"}
	}
	if err := validateObjectFields("session.audio", audio, allowed...); err != nil {
		return err
	}

	if raw, ok := audio["input"]; ok {
		var input map[string]jsontext.Value
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
			config.turnDetectionSet = true
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
		var output map[string]jsontext.Value
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

func parseVoice(data jsontext.Value) (string, error) {
	var voice string
	if data.Kind() == '"' && json.Unmarshal(data, &voice) == nil {
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

func parseTranscription(data jsontext.Value) (*provider.RealtimeTranscription, error) {
	if data.Kind() == 'n' {
		return nil, nil
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, errors.New("must be an object or null")
	}
	if err := validateObjectFields("session audio transcription", fields, "model", "language", "languages", "prompt", "delay"); err != nil {
		return nil, err
	}
	var value struct {
		Model     string   `json:"model"`
		Language  string   `json:"language"`
		Languages []string `json:"languages"`
		Prompt    string   `json:"prompt"`
		Delay     string   `json:"delay"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, errors.New("must be an object or null")
	}
	if value.Model == "" {
		value.Model = "gpt-live-transcribe"
	}
	return &provider.RealtimeTranscription{
		Model: value.Model, Language: value.Language, Languages: value.Languages,
		Prompt: value.Prompt, Delay: value.Delay,
	}, nil
}

func parseTurnDetection(data jsontext.Value) (*provider.RealtimeTurnDetection, error) {
	if data.Kind() == 'n' {
		return nil, nil
	}
	var fields map[string]jsontext.Value
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

func parseNoiseReduction(data jsontext.Value) (provider.RealtimeNoiseReduction, error) {
	if data.Kind() == 'n' {
		return "", nil
	}
	var fields map[string]jsontext.Value
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

func parseTruncation(data jsontext.Value) (*provider.RealtimeTruncation, error) {
	var mode string
	if data.Kind() == '"' && json.Unmarshal(data, &mode) == nil {
		switch mode {
		case "auto":
			return nil, nil
		case "disabled":
			return &provider.RealtimeTruncation{Disabled: true}, nil
		default:
			return nil, fmt.Errorf("unsupported mode %q", mode)
		}
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, errors.New("must be an object or \"auto\" or \"disabled\"")
	}
	if err := validateObjectFields("truncation", fields, "type", "retention_ratio", "token_limits"); err != nil {
		return nil, err
	}
	if raw, ok := fields["token_limits"]; ok {
		var tokenLimits map[string]jsontext.Value
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

func parseMetadata(data jsontext.Value) (map[string]string, error) {
	if data.Kind() == 'n' {
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

func validateObjectFields(context string, fields map[string]jsontext.Value, allowed ...string) error {
	for name := range fields {
		if !slices.Contains(allowed, name) {
			return fmt.Errorf("%s field %q is not supported", context, name)
		}
	}
	return nil
}

func parseAudioFormat(data jsontext.Value, current provider.RealtimeAudioFormat) (provider.RealtimeAudioFormat, error) {
	var legacy string
	if data.Kind() == '"' && json.Unmarshal(data, &legacy) == nil {
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
	var fields map[string]jsontext.Value
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

func parseMaxTokens(data jsontext.Value) (*int, error) {
	var value int
	if data.Kind() == '0' && json.Unmarshal(data, &value) == nil {
		if value < 1 || value > 4096 {
			return nil, errors.New("max_output_tokens must be between 1 and 4096")
		}
		return &value, nil
	}

	var unlimited string
	if data.Kind() == '"' && json.Unmarshal(data, &unlimited) == nil && unlimited == "inf" {
		return nil, nil
	}

	return nil, errors.New("max_output_tokens must be a positive integer or \"inf\"")
}

func parseTools(data jsontext.Value) ([]provider.Tool, error) {
	var fields []map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, errors.New("tools must be an array")
	}
	if fields == nil {
		return nil, errors.New("tools must be an array")
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
	if err := jsonutil.Unmarshal(data, &values); err != nil {
		return nil, errors.New("tools must be an array")
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

func parseToolChoice(data jsontext.Value) (*provider.ToolOptions, error) {
	if data.Kind() == '{' {
		var named struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(data, &named); err != nil || (named.Type != "function" && named.Type != "mcp") {
			return nil, errors.New("tool_choice object must be a function or mcp choice")
		}
		options := &provider.ToolOptions{Choice: provider.ToolChoiceAny}
		if named.Type == "function" && named.Name != "" {
			options.Allowed = []string{named.Name}
		}
		return options, nil
	}

	var choice string
	if json.Unmarshal(data, &choice) != nil {
		return nil, errors.New("tool_choice must be a string or an object")
	}

	switch choice {
	case "none":
		return &provider.ToolOptions{Choice: provider.ToolChoiceNone}, nil
	case "required", "any":
		return &provider.ToolOptions{Choice: provider.ToolChoiceAny}, nil
	case "auto":
		return &provider.ToolOptions{Choice: provider.ToolChoiceAuto}, nil
	}

	return nil, fmt.Errorf("tool_choice %q is unsupported", choice)
}

func parseResponseInput(data jsontext.Value) ([]provider.Message, error) {
	var items []conversationItem
	if err := json.Unmarshal(data, &items); err != nil || items == nil {
		return nil, errors.New("response.input must be an array of items")
	}

	input := make([]provider.Message, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "item_reference":
			input = append(input, provider.Message{ID: item.ID})

		case "function_call":
			call := provider.ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments}
			input = append(input, provider.Message{ID: item.ID, Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(call)}})

		case "function_call_output":
			result := provider.ToolResult{ID: item.CallID, Parts: []provider.Part{{Text: item.Output}}}
			input = append(input, provider.Message{ID: item.ID, Role: provider.MessageRoleUser, Content: []provider.Content{provider.ToolResultContent(result)}})

		case "message":
			if item.audio() != "" {
				return nil, errors.New("response.input messages cannot contain audio")
			}
			message, err := item.message()
			if err != nil {
				return nil, err
			}
			input = append(input, message)

		default:
			return nil, fmt.Errorf("response.input item type %q is unsupported", item.Type)
		}
	}

	return input, nil
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

// providerTurnDetection passes the client's choice through. Before the client
// chooses it is nil, so the provider keeps its own default.
func (c sessionConfig) providerTurnDetection() *provider.RealtimeTurnDetection {
	if !c.turnDetectionSet {
		return nil
	}
	if c.TurnDetection == nil {
		return &provider.RealtimeTurnDetection{Type: provider.RealtimeTurnDetectionNone}
	}
	return cloneTurnDetection(c.TurnDetection)
}

// declaredTurnDetection reports a provider's default in session terms, where
// nil means off. A provider that declares none leaves it to its service and is
// reported with the protocol's default, server VAD.
func declaredTurnDetection(defaults *provider.RealtimeTurnDetection) *provider.RealtimeTurnDetection {
	switch {
	case defaults == nil:
		return &provider.RealtimeTurnDetection{
			Type: provider.RealtimeTurnDetectionServer, CreateResponse: true, InterruptResponse: true,
		}
	case defaults.Disabled():
		return nil
	}
	return cloneTurnDetection(defaults)
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
	if len(value.Languages) > 0 {
		result["languages"] = value.Languages
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
	if c.Type == sessionTypeTranslation {
		return map[string]any{
			"id":      id,
			"type":    sessionTypeTranslation,
			"model":   model,
			"include": nil,
			"audio": map[string]any{
				"input": map[string]any{
					"transcription":   transcriptionObject(c.Transcription),
					"noise_reduction": noiseReductionObject(c.NoiseReduction),
				},
				"output": map[string]any{"language": c.Language},
			},
		}
	}

	if c.Type == sessionTypeTranscription {
		turnDetection := turnDetectionObject(c.TurnDetection)
		if detection, ok := turnDetection.(map[string]any); ok {
			delete(detection, "create_response")
			delete(detection, "interrupt_response")
		}

		return map[string]any{
			"id":      id,
			"object":  "realtime.transcription_session",
			"type":    sessionTypeTranscription,
			"include": nil,
			"audio": map[string]any{
				"input": map[string]any{
					"format":          audioFormatObject(c.InputAudio),
					"transcription":   transcriptionObject(c.Transcription),
					"noise_reduction": noiseReductionObject(c.NoiseReduction),
					"turn_detection":  turnDetection,
				},
			},
		}
	}

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
