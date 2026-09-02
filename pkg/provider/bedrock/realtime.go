package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/google/uuid"
)

const (
	defaultRealtimeVoice = "matthew"
	defaultInstructions  = "You are a helpful voice assistant. Respond naturally and concisely."
	credentialTimeout    = 5 * time.Second
	// Nova permits at most 50 KB in one textInput event. Leave a little
	// headroom for implementations that interpret KB as 1000 rather than 1024.
	maxRealtimeTextInputBytes = 48 * 1024
)

var _ provider.Realtime = (*Realtime)(nil)

type bidirectionalStream interface {
	Send(context.Context, types.InvokeModelWithBidirectionalStreamInput) error
	Events() <-chan types.InvokeModelWithBidirectionalStreamOutput
	Close() error
	Err() error
}

type Realtime struct {
	*Config

	credentials aws.CredentialsProvider
	open        func(context.Context, *bedrockruntime.InvokeModelWithBidirectionalStreamInput) (bidirectionalStream, error)
}

func NewRealtime(model string, options ...Option) (*Realtime, error) {
	cfg := &Config{
		model:  model,
		client: provider.DefaultClient,
		voice:  defaultRealtimeVoice,
	}

	for _, option := range options {
		option(cfg)
	}

	var loadOptions []func(*awsconfig.LoadOptions) error

	if cfg.client != nil {
		loadOptions = append(loadOptions, awsconfig.WithHTTPClient(cfg.client))
	}

	if cfg.region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(cfg.region))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("bedrock realtime: load AWS configuration: %w", err)
	}
	if awsCfg.Region == "" {
		return nil, errors.New("bedrock realtime: AWS region is not configured; set vars.region, AWS_REGION, AWS_DEFAULT_REGION, or an AWS profile region")
	}

	client := bedrockruntime.NewFromConfig(awsCfg, func(options *bedrockruntime.Options) {
		// AWS_BEARER_TOKEN_BEDROCK changes the service-wide preference to bearer
		// auth, but bidirectional event frames require a SigV4 seed signature.
		// Keep API-key auth available to the other Bedrock adapters while forcing
		// the only viable scheme for Nova Sonic sessions.
		options.AuthSchemePreference = []string{"sigv4"}
	})

	return &Realtime{
		Config:      cfg,
		credentials: awsCfg.Credentials,
		open: func(ctx context.Context, input *bedrockruntime.InvokeModelWithBidirectionalStreamInput) (bidirectionalStream, error) {
			output, err := client.InvokeModelWithBidirectionalStream(ctx, input)
			if err != nil {
				return nil, err
			}

			return output.GetStream(), nil
		},
	}, nil
}

func (r *Realtime) Defaults() provider.RealtimeOptions {
	maxTokens := 1024
	temperature := float32(0.7)
	topP := float32(0.9)

	voice := r.voice
	if voice == "" {
		voice = defaultRealtimeVoice
	}

	return provider.RealtimeOptions{
		Instructions: defaultInstructions,
		Voice:        voice,

		InputAudio: provider.RealtimeAudioFormat{
			Encoding:   provider.RealtimeAudioPCM,
			SampleRate: 24000,
			SampleSize: 16,
			Channels:   1,
		},
		OutputAudio: provider.RealtimeAudioFormat{
			Encoding:   provider.RealtimeAudioPCM,
			SampleRate: 24000,
			SampleSize: 16,
			Channels:   1,
		},

		MaxTokens:   &maxTokens,
		Temperature: &temperature,
		TopP:        &topP,

		ToolChoice: provider.ToolChoiceAuto,

		OutputModalities: []provider.RealtimeModality{provider.RealtimeModalityAudio},
		TurnDetection: &provider.RealtimeTurnDetection{
			Type:              provider.RealtimeTurnDetectionServer,
			CreateResponse:    true,
			InterruptResponse: true,
		},
		OutputTranscription: true,
	}
}

func (r *Realtime) Capabilities() provider.RealtimeCapabilities {
	return provider.RealtimeCapabilities{}
}

func (r *Realtime) Connect(ctx context.Context, options *provider.RealtimeOptions) (provider.RealtimeSession, error) {
	resolved := mergeRealtimeOptions(r.Defaults(), options)

	if err := validateRealtimeOptions(resolved); err != nil {
		return nil, err
	}

	// Bedrock API keys deliberately do not support the bidirectional stream
	// used by Nova Sonic. Resolve a standard SigV4 identity up front so a
	// bearer-key-only setup fails clearly instead of hanging on the first audio
	// frame while the SDK searches the ambient credential chain.
	credentialCtx, cancelCredentials := context.WithTimeout(ctx, credentialTimeout)
	defer cancelCredentials()
	if r.credentials == nil {
		return nil, errors.New("bedrock realtime: Nova Sonic requires standard AWS SigV4 credentials; Bedrock API keys do not support InvokeModelWithBidirectionalStream")
	}
	if _, err := r.credentials.Retrieve(credentialCtx); err != nil {
		return nil, fmt.Errorf("bedrock realtime: Nova Sonic requires standard AWS SigV4 credentials (AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY or AWS_PROFILE); Bedrock API keys do not support InvokeModelWithBidirectionalStream: %w", err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := r.open(streamCtx, &bedrockruntime.InvokeModelWithBidirectionalStreamInput{
		ModelId: aws.String(r.model),
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("bedrock realtime: %w", err)
	}

	session := &realtimeSession{
		ctx:    streamCtx,
		cancel: cancel,

		stream: stream,
		events: make(chan provider.RealtimeEvent, 64),

		options: resolved,

		promptName:       uuid.NewString(),
		audioContentName: uuid.NewString(),
	}

	go session.receive()

	if err := session.start(streamCtx); err != nil {
		_ = session.Close()
		return nil, err
	}

	return session, nil
}

func mergeRealtimeOptions(defaults provider.RealtimeOptions, options *provider.RealtimeOptions) provider.RealtimeOptions {
	if options == nil {
		return defaults
	}

	result := *options

	if result.Voice == "" {
		result.Voice = defaults.Voice
	}
	if result.InputAudio.Encoding == "" {
		result.InputAudio = defaults.InputAudio
	}
	if result.OutputAudio.Encoding == "" {
		result.OutputAudio = defaults.OutputAudio
	}
	if result.MaxTokens == nil {
		result.MaxTokens = defaults.MaxTokens
	}
	if result.Temperature == nil {
		result.Temperature = defaults.Temperature
	}
	if result.TopP == nil {
		result.TopP = defaults.TopP
	}
	if result.ToolChoice == "" {
		result.ToolChoice = defaults.ToolChoice
	}
	if len(result.OutputModalities) == 0 {
		result.OutputModalities = defaults.OutputModalities
	}

	return result
}

func validateRealtimeOptions(options provider.RealtimeOptions) error {
	for name, format := range map[string]provider.RealtimeAudioFormat{
		"input":  options.InputAudio,
		"output": options.OutputAudio,
	} {
		if format.Encoding != provider.RealtimeAudioPCM {
			return fmt.Errorf("bedrock realtime: %s audio encoding %q is unsupported; Nova Sonic requires linear PCM", name, format.Encoding)
		}

		if format.SampleRate != 8000 && format.SampleRate != 16000 && format.SampleRate != 24000 {
			return fmt.Errorf("bedrock realtime: %s sample rate %d is unsupported", name, format.SampleRate)
		}

		if format.SampleSize != 16 {
			return fmt.Errorf("bedrock realtime: %s sample size must be 16 bits", name)
		}

		if format.Channels != 1 {
			return fmt.Errorf("bedrock realtime: %s audio must be mono", name)
		}
	}

	return nil
}

type realtimeSession struct {
	ctx    context.Context
	cancel context.CancelFunc

	stream bidirectionalStream
	events chan provider.RealtimeEvent

	options provider.RealtimeOptions
	output  novaOutputTracker

	promptName       string
	audioContentName string

	sendMu    sync.Mutex
	closeOnce sync.Once
	closed    atomic.Bool
}

var _ provider.RealtimeSession = (*realtimeSession)(nil)

func (s *realtimeSession) start(ctx context.Context) error {
	configuration := map[string]any{}
	if s.options.MaxTokens != nil {
		configuration["maxTokens"] = *s.options.MaxTokens
	}
	if s.options.TopP != nil {
		configuration["topP"] = *s.options.TopP
	}
	if s.options.Temperature != nil {
		configuration["temperature"] = *s.options.Temperature
	}

	session := map[string]any{
		"inferenceConfiguration": configuration,
	}
	if s.options.TurnDetection != nil {
		session["turnDetectionConfiguration"] = map[string]any{
			"endpointingSensitivity": novaEndpointingSensitivity(s.options.TurnDetection),
		}
	}

	if err := s.send(ctx, "sessionStart", session); err != nil {
		return err
	}

	prompt := map[string]any{
		"promptName": s.promptName,
		"textOutputConfiguration": map[string]any{
			"mediaType": "text/plain",
		},
		"audioOutputConfiguration": audioConfiguration(s.options.OutputAudio, voiceForNova(s.options.Voice)),
	}

	if len(s.options.Tools) > 0 {
		prompt["toolUseOutputConfiguration"] = map[string]any{
			"mediaType": "application/json",
		}
		prompt["toolConfiguration"] = toolConfiguration(s.options.Tools, s.options.ToolChoice)
	}

	if err := s.send(ctx, "promptStart", prompt); err != nil {
		return err
	}

	if s.options.Instructions != "" {
		if err := s.sendTextBlock(ctx, provider.SystemMessage(s.options.Instructions), false); err != nil {
			return err
		}
	}

	for _, message := range s.options.History {
		if message.Text() == "" {
			continue
		}

		if err := s.sendTextBlock(ctx, message, false); err != nil {
			return err
		}
	}

	if err := s.send(ctx, "contentStart", map[string]any{
		"promptName":              s.promptName,
		"contentName":             s.audioContentName,
		"type":                    "AUDIO",
		"interactive":             true,
		"role":                    "USER",
		"audioInputConfiguration": audioConfiguration(s.options.InputAudio, ""),
	}); err != nil {
		return err
	}

	return nil
}

func novaEndpointingSensitivity(turn *provider.RealtimeTurnDetection) string {
	switch strings.ToLower(strings.TrimSpace(turn.Eagerness)) {
	case "high":
		return "HIGH"
	case "low":
		return "LOW"
	case "medium":
		return "MEDIUM"
	}

	if turn.SilenceDuration > 0 {
		switch {
		case turn.SilenceDuration <= 1500*time.Millisecond:
			return "HIGH"
		case turn.SilenceDuration >= 2*time.Second:
			return "LOW"
		}
	}

	return "MEDIUM"
}

func audioConfiguration(format provider.RealtimeAudioFormat, voice string) map[string]any {
	configuration := map[string]any{
		"mediaType":       "audio/lpcm",
		"sampleRateHertz": format.SampleRate,
		"sampleSizeBits":  format.SampleSize,
		"channelCount":    format.Channels,
		"encoding":        "base64",
		"audioType":       "SPEECH",
	}

	if voice != "" {
		configuration["voiceId"] = voice
	}

	return configuration
}

func toolConfiguration(tools []provider.Tool, choice provider.ToolChoice) map[string]any {
	nativeTools := make([]map[string]any, 0, len(tools))

	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}

		schema := tool.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		// The bidirectional Nova 2 endpoint currently models inputSchema.json
		// as a string containing JSON. This differs from Converse's Document
		// shape and from one version of the Nova tool guide; sending the object
		// directly is rejected by the live service as an unparseable chunk.
		encodedSchema, err := json.Marshal(schema)
		if err != nil {
			encodedSchema = []byte(`{"type":"object"}`)
		}

		nativeTools = append(nativeTools, map[string]any{
			"toolSpec": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"inputSchema": map[string]any{
					"json": string(encodedSchema),
				},
			},
		})
	}

	configuration := map[string]any{
		"tools": nativeTools,
	}

	switch choice {
	case provider.ToolChoiceAny:
		configuration["toolChoice"] = map[string]any{"any": map[string]any{}}
	case provider.ToolChoiceNone:
		// Nova Sonic does not have a native "none" tool choice. Omitting all
		// tools is the only reliable equivalent.
		configuration["tools"] = []map[string]any{}
	default:
		configuration["toolChoice"] = map[string]any{"auto": map[string]any{}}
	}

	return configuration
}

func voiceForNova(voice string) string {
	voice = strings.ToLower(strings.TrimSpace(voice))

	aliases := map[string]string{
		"alloy":   "tiffany",
		"ash":     "matthew",
		"ballad":  "matthew",
		"coral":   "tiffany",
		"echo":    "matthew",
		"fable":   "amy",
		"marin":   "tiffany",
		"nova":    "tiffany",
		"onyx":    "matthew",
		"sage":    "tiffany",
		"shimmer": "tiffany",
		"verse":   "matthew",
	}

	if native, ok := aliases[voice]; ok {
		return native
	}

	if voice == "" {
		return defaultRealtimeVoice
	}

	return voice
}

func (s *realtimeSession) SendAudio(ctx context.Context, audio []byte) error {
	if len(audio) == 0 {
		return nil
	}

	return s.send(ctx, "audioInput", map[string]any{
		"promptName":  s.promptName,
		"contentName": s.audioContentName,
		"content":     base64.StdEncoding.EncodeToString(audio),
	})
}

func (s *realtimeSession) CommitAudio(ctx context.Context) error {
	// Nova owns turn detection and keeps one audio content block open for the
	// entire session. A short silence gives its VAD an explicit push-to-talk
	// boundary without closing that content block.
	bytesPerSample := s.options.InputAudio.SampleSize / 8
	silence := make([]byte, s.options.InputAudio.SampleRate*s.options.InputAudio.Channels*bytesPerSample/2)

	return s.SendAudio(ctx, silence)
}

func (s *realtimeSession) Update(context.Context, *provider.RealtimeOptions) error {
	return provider.UnsupportedRealtimeOperation("session update")
}

func (s *realtimeSession) ClearAudio(context.Context) error {
	return provider.UnsupportedRealtimeOperation("audio buffer clearing")
}

func (s *realtimeSession) SendMessage(ctx context.Context, message provider.Message) error {
	return s.sendTextBlock(ctx, message, true)
}

func (s *realtimeSession) Respond(context.Context, *provider.RealtimeResponseOptions) error {
	// Interactive Nova content and VAD boundaries trigger generation natively.
	return nil
}

func (s *realtimeSession) Interrupt(context.Context) error {
	return provider.UnsupportedRealtimeOperation("explicit response interruption")
}

func (s *realtimeSession) sendTextBlock(ctx context.Context, message provider.Message, interactive bool) error {
	text := message.Text()
	if text == "" {
		return errors.New("bedrock realtime: text input is empty")
	}

	role := strings.ToUpper(string(message.Role))
	switch message.Role {
	case provider.MessageRoleSystem, provider.MessageRoleUser, provider.MessageRoleAssistant:
	default:
		return fmt.Errorf("bedrock realtime: unsupported text role %q", message.Role)
	}

	contentName := uuid.NewString()
	if err := s.send(ctx, "contentStart", map[string]any{
		"promptName":  s.promptName,
		"contentName": contentName,
		"type":        "TEXT",
		"interactive": interactive,
		"role":        role,
		"textInputConfiguration": map[string]any{
			"mediaType": "text/plain",
		},
	}); err != nil {
		return err
	}

	// Nova allows a large logical text content block, but each individual
	// textInput event is capped at 50 KB. Keep one contentStart/contentEnd pair
	// and stream UTF-8-safe chunks between them, as required by the Sonic API.
	for _, chunk := range splitRealtimeTextInput(text) {
		if err := s.send(ctx, "textInput", map[string]any{
			"promptName":  s.promptName,
			"contentName": contentName,
			"content":     chunk,
		}); err != nil {
			return err
		}
	}

	return s.send(ctx, "contentEnd", map[string]any{
		"promptName":  s.promptName,
		"contentName": contentName,
	})
}

func splitRealtimeTextInput(text string) []string {
	if len(text) <= maxRealtimeTextInputBytes {
		return []string{text}
	}

	chunks := make([]string, 0, (len(text)+maxRealtimeTextInputBytes-1)/maxRealtimeTextInputBytes)
	for len(text) > maxRealtimeTextInputBytes {
		end := maxRealtimeTextInputBytes
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		if end == 0 {
			// A valid UTF-8 rune is at most four bytes, so this is reachable only
			// for malformed input. Preserve progress and let JSON encoding replace
			// the malformed byte in the same way it otherwise would.
			end = maxRealtimeTextInputBytes
		}
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}

func (s *realtimeSession) SendToolResult(ctx context.Context, id, output string) error {
	if id == "" {
		return errors.New("bedrock realtime: tool use id is required")
	}

	if !json.Valid([]byte(output)) {
		data, err := json.Marshal(map[string]string{"result": output})
		if err != nil {
			return err
		}

		output = string(data)
	}

	contentName := uuid.NewString()
	if err := s.send(ctx, "contentStart", map[string]any{
		"promptName":  s.promptName,
		"contentName": contentName,
		"interactive": false,
		"type":        "TOOL",
		"role":        "TOOL",
		"toolResultInputConfiguration": map[string]any{
			"toolUseId": id,
			"type":      "TEXT",
			"textInputConfiguration": map[string]any{
				"mediaType": "text/plain",
			},
		},
	}); err != nil {
		return err
	}

	if err := s.send(ctx, "toolResult", map[string]any{
		"promptName":  s.promptName,
		"contentName": contentName,
		"content":     output,
	}); err != nil {
		return err
	}

	return s.send(ctx, "contentEnd", map[string]any{
		"promptName":  s.promptName,
		"contentName": contentName,
	})
}

func (s *realtimeSession) TruncateOutput(context.Context, string, time.Duration) error {
	// Nova's native interruption updates its conversation at the VAD boundary.
	return nil
}

func (s *realtimeSession) Events() <-chan provider.RealtimeEvent {
	return s.events
}

func (s *realtimeSession) Close() error {
	var result error

	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		result = errors.Join(result, s.send(ctx, "contentEnd", map[string]any{
			"promptName":  s.promptName,
			"contentName": s.audioContentName,
		}))
		result = errors.Join(result, s.send(ctx, "promptEnd", map[string]any{
			"promptName": s.promptName,
		}))
		result = errors.Join(result, s.send(ctx, "sessionEnd", map[string]any{}))

		s.closed.Store(true)
		result = errors.Join(result, s.stream.Close())
		s.cancel()
	})

	return result
}

func (s *realtimeSession) send(ctx context.Context, name string, payload any) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if s.closed.Load() {
		return errors.New("bedrock realtime: session is closed")
	}

	data, err := json.Marshal(map[string]any{
		"event": map[string]any{
			name: payload,
		},
	})
	if err != nil {
		return err
	}

	event := &types.InvokeModelWithBidirectionalStreamInputMemberChunk{
		Value: types.BidirectionalInputPayloadPart{
			Bytes: data,
		},
	}

	if err := s.stream.Send(ctx, event); err != nil {
		if streamErr := s.stream.Err(); streamErr != nil {
			return fmt.Errorf("bedrock realtime: send %s: %w (stream error: %v)", name, err, streamErr)
		}
		return fmt.Errorf("bedrock realtime: send %s: %w", name, err)
	}

	return nil
}

func (s *realtimeSession) receive() {
	defer close(s.events)

	for output := range s.stream.Events() {
		chunk, ok := output.(*types.InvokeModelWithBidirectionalStreamOutputMemberChunk)
		if !ok {
			continue
		}

		event, ok, err := parseRealtimeEvent(chunk.Value.Bytes)
		if err != nil {
			s.emit(provider.RealtimeEvent{Type: provider.RealtimeEventError, Err: err})
			continue
		}

		if ok {
			for _, normalized := range s.output.normalize(event) {
				s.emit(normalized)
			}
		}
	}

	if err := s.stream.Err(); err != nil && !s.closed.Load() {
		converted := convertError(err)
		s.emit(provider.RealtimeEvent{
			Type:  provider.RealtimeEventError,
			Error: normalizeBedrockRealtimeError(converted),
			Err:   fmt.Errorf("bedrock realtime stream: %w", converted),
		})
	}
}

func normalizeBedrockRealtimeError(err error) *provider.RealtimeError {
	if provider.TypeFromError(err) != "content_filter" {
		return nil
	}

	return &provider.RealtimeError{
		Type: "invalid_request_error",
		Code: "content_filter",
		Message: "Amazon Bedrock rejected the Nova Sonic session context because it matched a content filter. " +
			"The match can come from configured instructions, conversation history, or tool descriptions and may be a false positive; " +
			"it is not necessarily caused by what you just said.",
	}
}

type novaOutputContentState struct {
	typeName provider.RealtimeContentType
	role     provider.MessageRole
	stage    provider.RealtimeGenerationStage
}

// Nova 2 Sonic keeps one completion open for the full streaming session in
// current production behavior. OpenAI Realtime instead has a response
// lifecycle for every assistant/tool turn. Track Nova's speculative/final
// content pairs and synthesize that per-turn lifecycle here, before the events
// reach the protocol adapter.
type novaOutputTracker struct {
	sessionCompletionID string
	responseID          string
	active              bool

	contents map[string]novaOutputContentState

	speculativeTexts int
	finalTexts       int
	interrupted      bool
	usage            provider.RealtimeUsage
	pendingUsage     provider.RealtimeUsage
}

func (t *novaOutputTracker) normalize(event provider.RealtimeEvent) []provider.RealtimeEvent {
	switch event.Type {
	case provider.RealtimeEventResponseStarted:
		// completionStart identifies the long-lived Sonic session rather than
		// an individual conversational turn.
		t.sessionCompletionID = event.ResponseID
		return nil

	case provider.RealtimeEventContentStarted:
		result := t.ensureTurn()
		event.ResponseID = t.responseID
		if t.contents == nil {
			t.contents = make(map[string]novaOutputContentState)
		}
		t.contents[event.ContentID] = novaOutputContentState{
			typeName: event.ContentType,
			role:     event.Role,
			stage:    event.Stage,
		}
		return append(result, event)

	case provider.RealtimeEventTextDelta:
		result := t.ensureTurn()
		event.ResponseID = t.responseID
		state := t.contents[event.ContentID]
		if state.role == provider.MessageRoleAssistant {
			switch state.stage {
			case provider.RealtimeGenerationSpeculative:
				t.speculativeTexts++
			case provider.RealtimeGenerationFinal:
				t.finalTexts++
			}
		}
		return append(result, event)

	case provider.RealtimeEventAudioDelta, provider.RealtimeEventToolCall:
		result := t.ensureTurn()
		event.ResponseID = t.responseID
		return append(result, event)

	case provider.RealtimeEventUsage:
		if event.Usage == nil {
			return nil
		}
		if !t.active {
			addRealtimeUsage(&t.pendingUsage, event.Usage)
			return nil
		}
		addRealtimeUsage(&t.usage, event.Usage)
		usage := t.usage
		event.ResponseID = t.responseID
		event.Usage = &usage
		return []provider.RealtimeEvent{event}

	case provider.RealtimeEventInterrupted:
		result := t.ensureTurn()
		t.interrupted = true
		event.ResponseID = t.responseID
		return append(result, event)

	case provider.RealtimeEventContentDone:
		result := t.ensureTurn()
		event.ResponseID = t.responseID
		result = append(result, event)

		state := t.contents[event.ContentID]
		delete(t.contents, event.ContentID)

		finish := false
		if state.typeName == provider.RealtimeContentTool || strings.EqualFold(event.StopReason, "TOOL_USE") {
			// OpenAI clients execute function calls after response.done. Nova's
			// asynchronous follow-up speech becomes a subsequent response.
			finish = true
		} else if state.role == provider.MessageRoleAssistant && state.stage == provider.RealtimeGenerationFinal {
			finish = t.speculativeTexts > 0 && t.speculativeTexts == t.finalTexts
		} else if state.role == provider.MessageRoleAssistant && state.typeName == provider.RealtimeContentAudio {
			finish = t.speculativeTexts == 0 && strings.EqualFold(event.StopReason, "END_TURN")
		}
		if t.interrupted && isNovaInterrupted(event.StopReason) {
			finish = true
		}

		if finish {
			result = append(result, provider.RealtimeEvent{
				Type:       provider.RealtimeEventResponseDone,
				ResponseID: t.responseID,
				StopReason: event.StopReason,
			})
			t.resetTurn()
		}
		return result

	case provider.RealtimeEventResponseDone:
		if !t.active {
			return nil
		}
		event.ResponseID = t.responseID
		t.resetTurn()
		return []provider.RealtimeEvent{event}
	}

	return []provider.RealtimeEvent{event}
}

func (t *novaOutputTracker) ensureTurn() []provider.RealtimeEvent {
	if t.active {
		return nil
	}

	t.active = true
	t.responseID = "resp_nova_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.contents = make(map[string]novaOutputContentState)
	t.speculativeTexts = 0
	t.finalTexts = 0
	t.interrupted = false
	t.usage = t.pendingUsage
	t.pendingUsage = provider.RealtimeUsage{}

	result := []provider.RealtimeEvent{{
		Type:       provider.RealtimeEventResponseStarted,
		ResponseID: t.responseID,
	}}
	if t.usage != (provider.RealtimeUsage{}) {
		usage := t.usage
		result = append(result, provider.RealtimeEvent{
			Type: provider.RealtimeEventUsage, ResponseID: t.responseID, Usage: &usage,
		})
	}
	return result
}

func (t *novaOutputTracker) resetTurn() {
	t.responseID = ""
	t.active = false
	t.contents = make(map[string]novaOutputContentState)
	t.speculativeTexts = 0
	t.finalTexts = 0
	t.interrupted = false
	t.usage = provider.RealtimeUsage{}
}

func addRealtimeUsage(total *provider.RealtimeUsage, delta *provider.RealtimeUsage) {
	total.InputTokens += delta.InputTokens
	total.OutputTokens += delta.OutputTokens
	total.InputTextTokens += delta.InputTextTokens
	total.InputAudioTokens += delta.InputAudioTokens
	total.OutputTextTokens += delta.OutputTextTokens
	total.OutputAudioTokens += delta.OutputAudioTokens
}

func isNovaInterrupted(reason string) bool {
	reason = strings.ToLower(reason)
	return strings.Contains(reason, "interrupt") || strings.Contains(reason, "cancel")
}

func (s *realtimeSession) emit(event provider.RealtimeEvent) {
	select {
	case s.events <- event:
	case <-s.ctx.Done():
	}
}

type novaOutputEnvelope struct {
	Event struct {
		CompletionStart *novaCompletionStart `json:"completionStart"`
		ContentStart    *novaContentStart    `json:"contentStart"`
		TextOutput      *novaTextOutput      `json:"textOutput"`
		AudioOutput     *novaAudioOutput     `json:"audioOutput"`
		ToolUse         *novaToolUse         `json:"toolUse"`
		ContentEnd      *novaContentEnd      `json:"contentEnd"`
		CompletionEnd   *novaCompletionEnd   `json:"completionEnd"`
		Usage           *novaUsageEvent      `json:"usageEvent"`
	} `json:"event"`
}

type novaCompletionStart struct {
	CompletionID string `json:"completionId"`
}

type novaContentStart struct {
	AdditionalModelFields string `json:"additionalModelFields"`
	CompletionID          string `json:"completionId"`
	ContentID             string `json:"contentId"`
	Type                  string `json:"type"`
	Role                  string `json:"role"`
}

type novaTextOutput struct {
	CompletionID string `json:"completionId"`
	ContentID    string `json:"contentId"`
	Content      string `json:"content"`
}

type novaAudioOutput struct {
	CompletionID string `json:"completionId"`
	ContentID    string `json:"contentId"`
	Content      string `json:"content"`
}

type novaToolUse struct {
	CompletionID string `json:"completionId"`
	ContentID    string `json:"contentId"`
	Content      string `json:"content"`
	ToolName     string `json:"toolName"`
	ToolUseID    string `json:"toolUseId"`
}

type novaContentEnd struct {
	CompletionID string `json:"completionId"`
	ContentID    string `json:"contentId"`
	Type         string `json:"type"`
	StopReason   string `json:"stopReason"`
}

type novaCompletionEnd struct {
	CompletionID string `json:"completionId"`
	StopReason   string `json:"stopReason"`
}

type novaUsageEvent struct {
	CompletionID      string `json:"completionId"`
	TotalInputTokens  int    `json:"totalInputTokens"`
	TotalOutputTokens int    `json:"totalOutputTokens"`

	Details struct {
		Delta struct {
			Input  novaTokenDetails `json:"input"`
			Output novaTokenDetails `json:"output"`
		} `json:"delta"`
		Total struct {
			Input  novaTokenDetails `json:"input"`
			Output novaTokenDetails `json:"output"`
		} `json:"total"`
	} `json:"details"`
}

type novaTokenDetails struct {
	SpeechTokens int `json:"speechTokens"`
	TextTokens   int `json:"textTokens"`
}

func parseRealtimeEvent(data []byte) (provider.RealtimeEvent, bool, error) {
	var envelope novaOutputEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return provider.RealtimeEvent{}, false, fmt.Errorf("bedrock realtime: decode event: %w", err)
	}

	switch {
	case envelope.Event.CompletionStart != nil:
		value := envelope.Event.CompletionStart
		return provider.RealtimeEvent{
			Type:       provider.RealtimeEventResponseStarted,
			ResponseID: value.CompletionID,
		}, true, nil

	case envelope.Event.ContentStart != nil:
		value := envelope.Event.ContentStart
		stage := provider.RealtimeGenerationStage("")

		if value.AdditionalModelFields != "" {
			var fields struct {
				GenerationStage string `json:"generationStage"`
			}
			if err := json.Unmarshal([]byte(value.AdditionalModelFields), &fields); err == nil {
				stage = provider.RealtimeGenerationStage(strings.ToLower(fields.GenerationStage))
			}
		}

		return provider.RealtimeEvent{
			Type:        provider.RealtimeEventContentStarted,
			ResponseID:  value.CompletionID,
			ContentID:   value.ContentID,
			ContentType: provider.RealtimeContentType(strings.ToLower(value.Type)),
			Role:        provider.MessageRole(strings.ToLower(value.Role)),
			Stage:       stage,
		}, true, nil

	case envelope.Event.TextOutput != nil:
		value := envelope.Event.TextOutput
		var notification struct {
			Interrupted bool `json:"interrupted"`
		}

		if json.Unmarshal([]byte(value.Content), &notification) == nil && notification.Interrupted {
			return provider.RealtimeEvent{
				Type:       provider.RealtimeEventInterrupted,
				ResponseID: value.CompletionID,
				ContentID:  value.ContentID,
			}, true, nil
		}

		return provider.RealtimeEvent{
			Type:       provider.RealtimeEventTextDelta,
			ResponseID: value.CompletionID,
			ContentID:  value.ContentID,
			Text:       value.Content,
		}, true, nil

	case envelope.Event.AudioOutput != nil:
		value := envelope.Event.AudioOutput
		audio, err := base64.StdEncoding.DecodeString(value.Content)
		if err != nil {
			return provider.RealtimeEvent{}, false, fmt.Errorf("bedrock realtime: decode audio: %w", err)
		}

		return provider.RealtimeEvent{
			Type:       provider.RealtimeEventAudioDelta,
			ResponseID: value.CompletionID,
			ContentID:  value.ContentID,
			Audio:      audio,
		}, true, nil

	case envelope.Event.ToolUse != nil:
		value := envelope.Event.ToolUse
		return provider.RealtimeEvent{
			Type:       provider.RealtimeEventToolCall,
			ResponseID: value.CompletionID,
			ContentID:  value.ContentID,
			ToolCall: &provider.ToolCall{
				ID:        value.ToolUseID,
				Name:      value.ToolName,
				Arguments: value.Content,
			},
		}, true, nil

	case envelope.Event.ContentEnd != nil:
		value := envelope.Event.ContentEnd
		return provider.RealtimeEvent{
			Type:        provider.RealtimeEventContentDone,
			ResponseID:  value.CompletionID,
			ContentID:   value.ContentID,
			ContentType: provider.RealtimeContentType(strings.ToLower(value.Type)),
			StopReason:  value.StopReason,
		}, true, nil

	case envelope.Event.CompletionEnd != nil:
		value := envelope.Event.CompletionEnd
		return provider.RealtimeEvent{
			Type:       provider.RealtimeEventResponseDone,
			ResponseID: value.CompletionID,
			StopReason: value.StopReason,
		}, true, nil

	case envelope.Event.Usage != nil:
		value := envelope.Event.Usage
		return provider.RealtimeEvent{
			Type:       provider.RealtimeEventUsage,
			ResponseID: value.CompletionID,
			Usage: &provider.RealtimeUsage{
				InputTokens:  value.Details.Delta.Input.TextTokens + value.Details.Delta.Input.SpeechTokens,
				OutputTokens: value.Details.Delta.Output.TextTokens + value.Details.Delta.Output.SpeechTokens,

				InputTextTokens:  value.Details.Delta.Input.TextTokens,
				InputAudioTokens: value.Details.Delta.Input.SpeechTokens,

				OutputTextTokens:  value.Details.Delta.Output.TextTokens,
				OutputAudioTokens: value.Details.Delta.Output.SpeechTokens,
			},
		}, true, nil
	}

	return provider.RealtimeEvent{}, false, nil
}
