package google

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const defaultGeminiLiveURL = "https://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"

var _ provider.Realtime = (*Realtime)(nil)

type geminiRealtimeDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

// Realtime implements Gemini Live using its raw BidiGenerateContent WebSocket
// protocol. It intentionally does not expose Gemini message shapes above this
// package.
type Realtime struct {
	*Config

	url  string
	dial geminiRealtimeDialer
}

func NewRealtime(rawURL, model string, options ...Option) (*Realtime, error) {
	cfg := &Config{model: model, client: provider.DefaultClient}
	for _, option := range options {
		option(cfg)
	}
	if cfg.affectiveDialog && !supportsGeminiAffectiveDialog(model) {
		return nil, fmt.Errorf("gemini live: affective dialog is not supported by model %q", model)
	}
	if cfg.token == "" {
		cfg.token = os.Getenv("GEMINI_API_KEY")
		if cfg.token == "" {
			cfg.token = os.Getenv("GOOGLE_API_KEY")
		}
	}
	if rawURL == "" {
		rawURL = defaultGeminiLiveURL
	}

	dialer := geminiWebSocketDialer(cfg.client)
	return &Realtime{
		Config: cfg,
		url:    rawURL,
		dial: func(ctx context.Context, target string, headers http.Header) (*websocket.Conn, *http.Response, error) {
			return dialer.DialContext(ctx, target, headers)
		},
	}, nil
}

func (r *Realtime) Defaults() provider.RealtimeOptions {
	defaults := provider.RealtimeOptions{
		Voice: "Kore",
		InputAudio: provider.RealtimeAudioFormat{
			Encoding: provider.RealtimeAudioPCM, SampleRate: 16000, SampleSize: 16, Channels: 1,
		},
		OutputAudio: provider.RealtimeAudioFormat{
			Encoding: provider.RealtimeAudioPCM, SampleRate: 24000, SampleSize: 16, Channels: 1,
		},
		ToolChoice:       provider.ToolChoiceAuto,
		OutputModalities: []provider.RealtimeModality{provider.RealtimeModalityAudio},
		TurnDetection: &provider.RealtimeTurnDetection{
			Type:           provider.RealtimeTurnDetectionServer,
			CreateResponse: true, InterruptResponse: true,
		},
		OutputTranscription: true,
	}
	if isGeminiLiveTranscriptionModel(r.model) {
		defaults.OutputModalities = []provider.RealtimeModality{provider.RealtimeModalityText}
		defaults.InputTranscription = &provider.RealtimeTranscription{}
		defaults.OutputTranscription = false
	}
	return defaults
}

func (r *Realtime) Capabilities() provider.RealtimeCapabilities {
	return provider.RealtimeCapabilities{
		// Gemini reports automatic and manually-signaled activity using native
		// voiceActivity messages.
		InputActivityEvents: true,
		// Gemini 3.1 only accepts clientContent while seeding initial history;
		// unlike 2.5 it cannot use an empty clientContent update as an explicit
		// response.cancel. Native voice activity still supports barge-in.
		Interruption: supportsGeminiClientContentInterrupt(r.model),
	}
}

func (r *Realtime) Connect(ctx context.Context, options *provider.RealtimeOptions) (provider.RealtimeSession, error) {
	resolved := mergeGeminiRealtimeOptions(r.Defaults(), options)
	if err := validateGeminiRealtimeOptions(resolved); err != nil {
		return nil, err
	}
	if err := validateGeminiRealtimeModelOptions(r.model, resolved); err != nil {
		return nil, err
	}

	target, err := r.websocketURL()
	if err != nil {
		return nil, err
	}
	conn, response, err := r.dial(ctx, target, nil)
	if err != nil {
		if response != nil {
			if response.Body != nil {
				_ = response.Body.Close()
			}
			return nil, fmt.Errorf("gemini live: websocket handshake returned %s: %w", response.Status, err)
		}
		return nil, fmt.Errorf("gemini live: %w", err)
	}
	conn.SetReadLimit(32 << 20)

	sessionCtx, cancel := context.WithCancel(ctx)
	session := &geminiRealtimeSession{
		ctx:                    sessionCtx,
		cancel:                 cancel,
		conn:                   conn,
		events:                 make(chan provider.RealtimeEvent, 128),
		ready:                  make(chan error, 1),
		options:                resolved,
		toolNames:              make(map[string]string),
		clientContentInterrupt: supportsGeminiClientContentInterrupt(r.model),
	}
	go session.receive()

	if err := session.send(ctx, r.setup(resolved)); err != nil {
		_ = session.Close()
		return nil, err
	}

	select {
	case err := <-session.ready:
		if err != nil {
			_ = session.Close()
			return nil, err
		}
	case <-ctx.Done():
		_ = session.Close()
		return nil, ctx.Err()
	case <-time.After(15 * time.Second):
		_ = session.Close()
		return nil, errors.New("gemini live: timed out waiting for setupComplete")
	}

	if len(resolved.History) > 0 {
		turns := make([]map[string]any, 0, len(resolved.History))
		for _, message := range resolved.History {
			if message.Text() == "" || message.Role == provider.MessageRoleSystem {
				continue
			}
			role := "user"
			if message.Role == provider.MessageRoleAssistant {
				role = "model"
			}
			turns = append(turns, map[string]any{"role": role, "parts": []map[string]any{{"text": message.Text()}}})
		}
		if len(turns) > 0 {
			if err := session.send(ctx, map[string]any{"clientContent": map[string]any{"turns": turns, "turnComplete": true}}); err != nil {
				_ = session.Close()
				return nil, err
			}
		}
	}

	return session, nil
}

func (r *Realtime) setup(options provider.RealtimeOptions) map[string]any {
	message := geminiSetup(r.model, options)
	if r.affectiveDialog {
		message["setup"].(map[string]any)["enableAffectiveDialog"] = true
	}
	return message
}

func supportsGeminiAffectiveDialog(model string) bool {
	model = strings.ToLower(strings.TrimPrefix(model, "models/"))
	return strings.Contains(model, "gemini-2.5-flash") &&
		(strings.Contains(model, "live") || strings.Contains(model, "native-audio"))
}

func supportsGeminiClientContentInterrupt(model string) bool {
	// The Gemini 3.1 capability table limits clientContent to initial history.
	// Only the documented 2.5 Live family supports it throughout a session.
	return supportsGeminiAffectiveDialog(model)
}

func isGeminiLiveTranscriptionModel(model string) bool {
	model = strings.ToLower(strings.TrimPrefix(model, "models/"))
	return strings.Contains(model, "transcribe-live")
}

func (r *Realtime) websocketURL() (string, error) {
	u, err := url.Parse(r.url)
	if err != nil {
		return "", fmt.Errorf("gemini live: invalid URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("gemini live: unsupported URL scheme %q", u.Scheme)
	}
	query := u.Query()
	if r.token != "" {
		query.Set("key", r.token)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func geminiWebSocketDialer(client *http.Client) *websocket.Dialer {
	dialer := *websocket.DefaultDialer
	if client == nil {
		return &dialer
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		return &dialer
	}
	dialer.Proxy = transport.Proxy
	dialer.NetDialContext = transport.DialContext
	dialer.HandshakeTimeout = transport.TLSHandshakeTimeout
	if transport.TLSClientConfig != nil {
		dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
	} else {
		dialer.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &dialer
}

func mergeGeminiRealtimeOptions(defaults provider.RealtimeOptions, options *provider.RealtimeOptions) provider.RealtimeOptions {
	if options == nil {
		return defaults
	}
	result := *options
	result.Tools = slices.Clone(options.Tools)
	result.History = slices.Clone(options.History)
	result.OutputModalities = slices.Clone(options.OutputModalities)
	if options.TurnDetection != nil {
		turnDetection := *options.TurnDetection
		result.TurnDetection = &turnDetection
	}
	if options.InputTranscription != nil {
		transcription := *options.InputTranscription
		result.InputTranscription = &transcription
	}
	if result.Voice == "" {
		result.Voice = defaults.Voice
	}
	if result.InputAudio.Encoding == "" {
		result.InputAudio = defaults.InputAudio
	}
	if result.OutputAudio.Encoding == "" {
		result.OutputAudio = defaults.OutputAudio
	}
	if result.ToolChoice == "" {
		result.ToolChoice = defaults.ToolChoice
	}
	if len(result.OutputModalities) == 0 {
		result.OutputModalities = slices.Clone(defaults.OutputModalities)
	}
	return result
}

func validateGeminiRealtimeOptions(options provider.RealtimeOptions) error {
	if options.InputAudio.Encoding != provider.RealtimeAudioPCM || options.InputAudio.SampleSize != 16 || options.InputAudio.Channels != 1 || options.InputAudio.SampleRate <= 0 {
		return errors.New("gemini live: input audio must be mono 16-bit PCM with a declared sample rate")
	}
	if options.OutputAudio.Encoding != provider.RealtimeAudioPCM || options.OutputAudio.SampleSize != 16 || options.OutputAudio.Channels != 1 || options.OutputAudio.SampleRate != 24000 {
		return errors.New("gemini live: output audio must be mono 16-bit PCM at 24000 Hz")
	}
	if len(options.OutputModalities) != 1 {
		return errors.New("gemini live: exactly one output modality is required")
	}
	if options.TurnDetection != nil && !options.TurnDetection.CreateResponse {
		return errors.New("gemini live: automatic activity detection cannot disable automatic response creation")
	}
	if options.ToolChoice == provider.ToolChoiceAny {
		return errors.New("gemini live: required tool choice is not supported by the Live API")
	}
	return nil
}

func validateGeminiRealtimeModelOptions(model string, options provider.RealtimeOptions) error {
	if !isGeminiLiveTranscriptionModel(model) {
		return nil
	}
	if len(options.OutputModalities) != 1 || options.OutputModalities[0] != provider.RealtimeModalityText {
		return errors.New("gemini live transcription: response modality must be text")
	}
	if options.InputTranscription == nil {
		return errors.New("gemini live transcription: input audio transcription must be enabled")
	}
	if len(options.Tools) > 0 {
		return errors.New("gemini live transcription: tools are not supported by the dedicated transcription model")
	}
	return nil
}

func geminiSetup(model string, options provider.RealtimeOptions) map[string]any {
	modalities := make([]string, len(options.OutputModalities))
	for i, modality := range options.OutputModalities {
		modalities[i] = strings.ToUpper(string(modality))
	}
	generation := map[string]any{"responseModalities": modalities}
	if options.MaxTokens != nil {
		generation["maxOutputTokens"] = *options.MaxTokens
	}
	if options.Temperature != nil {
		generation["temperature"] = *options.Temperature
	}
	if options.TopP != nil {
		generation["topP"] = *options.TopP
	}
	if slices.Contains(options.OutputModalities, provider.RealtimeModalityAudio) && options.Voice != "" {
		generation["speechConfig"] = map[string]any{
			"voiceConfig": map[string]any{"prebuiltVoiceConfig": map[string]any{"voiceName": voiceForGemini(options.Voice)}},
		}
	}

	setup := map[string]any{
		"model":            "models/" + strings.TrimPrefix(model, "models/"),
		"generationConfig": generation,
	}
	if options.Instructions != "" {
		setup["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": options.Instructions}}}
	}
	if len(options.Tools) > 0 && options.ToolChoice != provider.ToolChoiceNone {
		functions := make([]map[string]any, 0, len(options.Tools))
		for _, tool := range options.Tools {
			if tool.Name == "" {
				continue
			}
			// parameters is Gemini's restricted protobuf Schema and rejects
			// otherwise-valid JSON Schema keywords used by Wingman tools. The
			// Value-typed parametersJsonSchema field preserves those schemas.
			functions = append(functions, map[string]any{
				"name": tool.Name, "description": tool.Description,
				"parametersJsonSchema": tool.Parameters,
			})
		}
		setup["tools"] = []map[string]any{{"functionDeclarations": functions}}
	}
	setup["realtimeInputConfig"] = geminiRealtimeInputConfig(options.TurnDetection)
	if options.InputTranscription != nil {
		transcription := map[string]any{}
		if language := strings.TrimSpace(options.InputTranscription.Language); language != "" {
			transcription["languageCodes"] = []string{language}
		}
		setup["inputAudioTranscription"] = transcription
	}
	if options.OutputTranscription || slices.Contains(options.OutputModalities, provider.RealtimeModalityAudio) {
		setup["outputAudioTranscription"] = map[string]any{}
	}
	if len(options.History) > 0 {
		setup["historyConfig"] = map[string]any{"initialHistoryInClientContent": true}
	}
	return map[string]any{"setup": setup}
}

func geminiRealtimeInputConfig(turn *provider.RealtimeTurnDetection) map[string]any {
	if turn == nil {
		return map[string]any{"automaticActivityDetection": map[string]any{"disabled": true}}
	}

	detection := map[string]any{"disabled": false}
	if turn.PrefixPadding > 0 {
		detection["prefixPaddingMs"] = turn.PrefixPadding.Milliseconds()
	}
	if turn.SilenceDuration > 0 {
		detection["silenceDurationMs"] = turn.SilenceDuration.Milliseconds()
	}
	switch strings.ToLower(turn.Eagerness) {
	case "high":
		detection["endOfSpeechSensitivity"] = "END_SENSITIVITY_HIGH"
	case "low":
		detection["endOfSpeechSensitivity"] = "END_SENSITIVITY_LOW"
	}

	activityHandling := "START_OF_ACTIVITY_INTERRUPTS"
	if !turn.InterruptResponse {
		activityHandling = "NO_INTERRUPTION"
	}
	return map[string]any{
		"automaticActivityDetection": detection,
		"activityHandling":           activityHandling,
	}
}

func voiceForGemini(voice string) string {
	trimmed := strings.TrimSpace(voice)
	switch strings.ToLower(trimmed) {
	case "alloy", "coral", "marin", "nova", "sage", "shimmer":
		return "Kore"
	case "ash", "ballad", "echo", "fable", "onyx", "verse":
		return "Puck"
	case "":
		return "Kore"
	default:
		return trimmed
	}
}

type geminiRealtimeSession struct {
	ctx                    context.Context
	cancel                 context.CancelFunc
	conn                   *websocket.Conn
	events                 chan provider.RealtimeEvent
	ready                  chan error
	options                provider.RealtimeOptions
	clientContentInterrupt bool

	sendMu    sync.Mutex
	stateMu   sync.Mutex
	closeOnce sync.Once
	closed    atomic.Bool
	toolNames map[string]string

	manualActivity bool

	responseID         string
	assistantItemID    string
	assistantContentID string
	assistantType      provider.RealtimeContentType
	assistantOpen      bool
	inputItemID        string
	inputContentID     string
	inputOpen          bool
	inputActivity      bool
	pendingInputText   string
	interrupted        bool
}

var _ provider.RealtimeSession = (*geminiRealtimeSession)(nil)

func (s *geminiRealtimeSession) Update(context.Context, *provider.RealtimeOptions) error {
	return provider.UnsupportedRealtimeOperation("session update")
}

func (s *geminiRealtimeSession) SendAudio(ctx context.Context, audio []byte) error {
	if len(audio) == 0 {
		return nil
	}
	if s.options.TurnDetection == nil {
		if err := s.beginManualActivity(ctx); err != nil {
			return err
		}
	}
	return s.send(ctx, map[string]any{"realtimeInput": map[string]any{"audio": map[string]any{
		"data":     base64.StdEncoding.EncodeToString(audio),
		"mimeType": fmt.Sprintf("audio/pcm;rate=%d", s.options.InputAudio.SampleRate),
	}}})
}

func (s *geminiRealtimeSession) CommitAudio(ctx context.Context) error {
	if s.options.TurnDetection == nil {
		return s.endManualActivity(ctx)
	}
	return s.send(ctx, map[string]any{"realtimeInput": map[string]any{"audioStreamEnd": true}})
}

func (s *geminiRealtimeSession) ClearAudio(context.Context) error {
	return provider.UnsupportedRealtimeOperation("audio buffer clearing")
}

func (s *geminiRealtimeSession) SendMessage(ctx context.Context, message provider.Message) error {
	text := message.Text()
	if text == "" {
		return errors.New("gemini live: text message is empty")
	}
	if message.Role != provider.MessageRoleUser {
		return fmt.Errorf("gemini live: interactive message role %q is unsupported", message.Role)
	}
	startedActivity := false
	if s.options.TurnDetection == nil {
		s.stateMu.Lock()
		startedActivity = !s.manualActivity
		s.stateMu.Unlock()
		if startedActivity {
			if err := s.beginManualActivity(ctx); err != nil {
				return err
			}
		}
	}
	if err := s.send(ctx, map[string]any{"realtimeInput": map[string]any{"text": text}}); err != nil {
		return err
	}
	if startedActivity {
		return s.endManualActivity(ctx)
	}
	return nil
}

func (s *geminiRealtimeSession) beginManualActivity(ctx context.Context) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.manualActivity {
		return nil
	}
	if err := s.send(ctx, map[string]any{"realtimeInput": map[string]any{"activityStart": map[string]any{}}}); err != nil {
		return err
	}
	s.manualActivity = true
	return nil
}

func (s *geminiRealtimeSession) endManualActivity(ctx context.Context) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if !s.manualActivity {
		return nil
	}
	if err := s.send(ctx, map[string]any{"realtimeInput": map[string]any{"activityEnd": map[string]any{}}}); err != nil {
		return err
	}
	s.manualActivity = false
	return nil
}

func (s *geminiRealtimeSession) SendToolResult(ctx context.Context, id, output string) error {
	s.stateMu.Lock()
	name := s.toolNames[id]
	s.stateMu.Unlock()
	if id == "" || name == "" {
		return errors.New("gemini live: unknown tool call id")
	}
	var result any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		result = output
	}
	if err := s.send(ctx, map[string]any{"toolResponse": map[string]any{"functionResponses": []map[string]any{{
		"id": id, "name": name, "response": map[string]any{"result": result},
	}}}}); err != nil {
		return err
	}
	s.stateMu.Lock()
	delete(s.toolNames, id)
	s.stateMu.Unlock()
	return nil
}

func (s *geminiRealtimeSession) TruncateOutput(context.Context, string, time.Duration) error {
	// Gemini applies barge-in to its conversation when realtime activity starts.
	return nil
}

func (s *geminiRealtimeSession) Respond(context.Context, *provider.RealtimeResponseOptions) error {
	// realtimeInput text/audio, activityEnd, and tool responses trigger turns.
	return nil
}

func (s *geminiRealtimeSession) Interrupt(ctx context.Context) error {
	if !s.clientContentInterrupt {
		return provider.UnsupportedRealtimeOperation("explicit response interruption for this Gemini Live model")
	}
	// ClientContent interrupts an in-flight generation. Its turns field is
	// optional, so this does not add a synthetic user message to history.
	return s.send(ctx, map[string]any{"clientContent": map[string]any{"turnComplete": false}})
}

func (s *geminiRealtimeSession) Events() <-chan provider.RealtimeEvent { return s.events }

func (s *geminiRealtimeSession) Close() error {
	var result error
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.cancel()
		deadline := time.Now().Add(time.Second)
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
		result = s.conn.Close()
	})
	return result
}

func (s *geminiRealtimeSession) send(ctx context.Context, event any) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.closed.Load() {
		return errors.New("gemini live: session is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.conn.SetWriteDeadline(deadline)
		defer s.conn.SetWriteDeadline(time.Time{})
	}
	if err := s.conn.WriteJSON(event); err != nil {
		return fmt.Errorf("gemini live: write event: %w", err)
	}
	return nil
}

func (s *geminiRealtimeSession) receive() {
	defer close(s.events)
	ready := false
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			if !ready {
				s.signalReady(fmt.Errorf("gemini live: read setupComplete: %w", err))
			}
			if !s.closed.Load() {
				s.emit(provider.RealtimeEvent{Type: provider.RealtimeEventError, Err: fmt.Errorf("gemini live: read event: %w", err)})
			}
			return
		}

		var message geminiServerMessage
		if err := json.Unmarshal(data, &message); err != nil {
			s.emit(provider.RealtimeEvent{Type: provider.RealtimeEventError, Err: fmt.Errorf("gemini live: decode event: %w", err)})
			continue
		}
		if message.SetupComplete != nil && !ready {
			ready = true
			s.signalReady(nil)
			continue
		}
		for _, event := range s.translate(message) {
			s.emit(event)
		}
	}
}

func (s *geminiRealtimeSession) signalReady(err error) {
	select {
	case s.ready <- err:
	default:
	}
}

func (s *geminiRealtimeSession) emit(event provider.RealtimeEvent) {
	select {
	case s.events <- event:
	case <-s.ctx.Done():
	}
}

type geminiServerMessage struct {
	SetupComplete *struct{} `json:"setupComplete"`
	ServerContent *struct {
		ModelTurn *struct {
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					Data     string `json:"data"`
					MimeType string `json:"mimeType"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"modelTurn"`
		GenerationComplete        bool                 `json:"generationComplete"`
		TurnComplete              bool                 `json:"turnComplete"`
		Interrupted               bool                 `json:"interrupted"`
		InputTranscription        *geminiTranscription `json:"inputTranscription"`
		InterimInputTranscription *geminiTranscription `json:"interimInputTranscription"`
		OutputTranscription       *geminiTranscription `json:"outputTranscription"`
	} `json:"serverContent"`
	ToolCall *struct {
		FunctionCalls []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Args any    `json:"args"`
		} `json:"functionCalls"`
	} `json:"toolCall"`
	ToolCallCancellation *struct {
		IDs []string `json:"ids"`
	} `json:"toolCallCancellation"`
	VoiceActivity *struct {
		Type string `json:"voiceActivityType"`
	} `json:"voiceActivity"`
	UsageMetadata *struct {
		PromptTokenCount    int `json:"promptTokenCount"`
		ResponseTokenCount  int `json:"responseTokenCount"`
		PromptTokensDetails []struct {
			Modality   string `json:"modality"`
			TokenCount int    `json:"tokenCount"`
		} `json:"promptTokensDetails"`
		ResponseTokensDetails []struct {
			Modality   string `json:"modality"`
			TokenCount int    `json:"tokenCount"`
		} `json:"responseTokensDetails"`
	} `json:"usageMetadata"`
}

type geminiTranscription struct {
	Text         string `json:"text"`
	LanguageCode string `json:"languageCode"`
}

func (s *geminiRealtimeSession) translate(message geminiServerMessage) []provider.RealtimeEvent {
	var events []provider.RealtimeEvent
	usageEmitted := false
	appendUsage := func() {
		if usageEmitted || message.UsageMetadata == nil {
			return
		}
		usage := &provider.RealtimeUsage{InputTokens: message.UsageMetadata.PromptTokenCount, OutputTokens: message.UsageMetadata.ResponseTokenCount}
		for _, detail := range message.UsageMetadata.PromptTokensDetails {
			switch strings.ToUpper(detail.Modality) {
			case "AUDIO":
				usage.InputAudioTokens += detail.TokenCount
			case "TEXT":
				usage.InputTextTokens += detail.TokenCount
			}
		}
		for _, detail := range message.UsageMetadata.ResponseTokensDetails {
			switch strings.ToUpper(detail.Modality) {
			case "AUDIO":
				usage.OutputAudioTokens += detail.TokenCount
			case "TEXT":
				usage.OutputTextTokens += detail.TokenCount
			}
		}
		events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventUsage, ResponseID: s.responseID, Usage: usage})
		usageEmitted = true
	}
	if message.VoiceActivity != nil {
		events = append(events, s.translateVoiceActivity(message.VoiceActivity.Type)...)
	}
	if message.ServerContent != nil {
		content := message.ServerContent
		if content.InterimInputTranscription != nil && content.InterimInputTranscription.Text != "" {
			events = append(events, provider.RealtimeEvent{
				Type: provider.RealtimeEventInputTranscriptionPreview,
				Text: content.InterimInputTranscription.Text, Stage: provider.RealtimeGenerationSpeculative,
			})
		}
		if content.InputTranscription != nil && content.InputTranscription.Text != "" {
			if s.inputActivity {
				// inputTranscription is a finalized snapshot. Hold it only until the
				// matching activity-end so OpenAI's committed/item ordering remains
				// intact; do not wait for the assistant's turnComplete.
				s.pendingInputText = content.InputTranscription.Text
			} else {
				events = append(events, s.finalizeInput(content.InputTranscription.Text)...)
			}
		}
		if content.ModelTurn != nil {
			for _, part := range content.ModelTurn.Parts {
				if part.InlineData != nil && part.InlineData.Data != "" {
					events = append(events, s.ensureAssistant(provider.RealtimeContentAudio)...)
					audio, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
					if err != nil {
						events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventError, Err: fmt.Errorf("gemini live: decode audio: %w", err)})
					} else {
						events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventAudioDelta, ResponseID: s.responseID, ContentID: s.assistantContentID, ItemID: s.assistantItemID, Audio: audio})
					}
				}
				if part.Text != "" {
					events = append(events, s.ensureAssistant(provider.RealtimeContentText)...)
					events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ResponseID: s.responseID, ContentID: s.assistantContentID, ItemID: s.assistantItemID, Text: part.Text})
				}
			}
		}
		if content.OutputTranscription != nil && content.OutputTranscription.Text != "" {
			events = append(events, s.ensureAssistant(provider.RealtimeContentAudio)...)
			events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ResponseID: s.responseID, ContentID: s.assistantContentID, ItemID: s.assistantItemID, Text: content.OutputTranscription.Text})
		}
		if content.Interrupted {
			events = append(events, s.ensureResponse()...)
			s.interrupted = true
			events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventInterrupted, ResponseID: s.responseID})
		}
		if content.GenerationComplete || content.Interrupted {
			events = append(events, s.finishAssistant()...)
		}
		if content.TurnComplete {
			if s.inputActivity {
				events = append(events, s.translateVoiceActivity("ACTIVITY_END")...)
			} else {
				events = append(events, s.flushPendingInput()...)
			}
			events = append(events, s.finishInput()...)
			events = append(events, s.finishAssistant()...)
			if s.responseID != "" {
				appendUsage()
				stopReason := "completed"
				if s.interrupted {
					stopReason = "interrupted"
				}
				events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: s.responseID, StopReason: stopReason})
			}
			s.resetResponse()
		}
	}

	if message.ToolCall != nil {
		events = append(events, s.ensureResponse()...)
		for _, call := range message.ToolCall.FunctionCalls {
			args := call.Args
			if args == nil {
				args = map[string]any{}
			}
			arguments, err := json.Marshal(args)
			if err != nil {
				arguments = []byte("{}")
			}
			s.stateMu.Lock()
			s.toolNames[call.ID] = call.Name
			s.stateMu.Unlock()
			contentID := "tool_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			itemID := "item_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			events = append(events,
				provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ResponseID: s.responseID, ContentID: contentID, ItemID: itemID, ContentType: provider.RealtimeContentTool, Role: provider.MessageRoleAssistant},
				provider.RealtimeEvent{Type: provider.RealtimeEventToolCall, ResponseID: s.responseID, ContentID: contentID, ItemID: itemID, ToolCall: &provider.ToolCall{ID: call.ID, Name: call.Name, Arguments: string(arguments)}},
				provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ResponseID: s.responseID, ContentID: contentID, ItemID: itemID, ContentType: provider.RealtimeContentTool},
			)
		}
		appendUsage()
		events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: s.responseID, StopReason: "tool_call"})
		s.resetResponse()
	}

	if message.ToolCallCancellation != nil && len(message.ToolCallCancellation.IDs) > 0 {
		// Cancellation is scoped to the named calls and accompanies an actual
		// interrupted server turn. Remove them so a late result is rejected, but
		// do not manufacture a second response/audio interruption event.
		s.stateMu.Lock()
		for _, id := range message.ToolCallCancellation.IDs {
			delete(s.toolNames, id)
		}
		s.stateMu.Unlock()
	}
	appendUsage()
	return events
}

func (s *geminiRealtimeSession) translateVoiceActivity(activityType string) []provider.RealtimeEvent {
	switch strings.ToUpper(activityType) {
	case "ACTIVITY_START":
		if s.inputActivity {
			return nil
		}
		s.inputActivity = true
		s.pendingInputText = ""
		s.inputItemID = "item_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		s.inputContentID = ""
		s.inputOpen = false
		return []provider.RealtimeEvent{{
			Type: provider.RealtimeEventInputSpeechStarted, ItemID: s.inputItemID,
		}}

	case "ACTIVITY_END":
		if !s.inputActivity && s.inputItemID == "" {
			return s.flushPendingInput()
		}
		itemID := s.inputItemID
		if itemID == "" {
			itemID = "item_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			s.inputItemID = itemID
		}
		s.inputActivity = false
		events := []provider.RealtimeEvent{
			{Type: provider.RealtimeEventInputSpeechStopped, ItemID: itemID},
			{Type: provider.RealtimeEventInputCommitted, ItemID: itemID},
		}
		return append(events, s.flushPendingInput()...)
	}
	return nil
}

func (s *geminiRealtimeSession) finalizeInput(text string) []provider.RealtimeEvent {
	if text == "" {
		return nil
	}
	events := s.ensureInput()
	events = append(events, provider.RealtimeEvent{
		Type: provider.RealtimeEventTextDelta, ContentID: s.inputContentID,
		ItemID: s.inputItemID, Text: text,
	})
	return append(events, s.finishInput()...)
}

func (s *geminiRealtimeSession) flushPendingInput() []provider.RealtimeEvent {
	text := s.pendingInputText
	s.pendingInputText = ""
	return s.finalizeInput(text)
}

func (s *geminiRealtimeSession) ensureResponse() []provider.RealtimeEvent {
	if s.responseID != "" {
		return nil
	}
	s.responseID = "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	return []provider.RealtimeEvent{{Type: provider.RealtimeEventResponseStarted, ResponseID: s.responseID}}
}

func (s *geminiRealtimeSession) ensureAssistant(contentType provider.RealtimeContentType) []provider.RealtimeEvent {
	events := s.ensureResponse()
	if s.assistantOpen {
		return events
	}
	s.assistantItemID = "item_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	s.assistantContentID = "content_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	s.assistantType = contentType
	s.assistantOpen = true
	return append(events, provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ResponseID: s.responseID, ContentID: s.assistantContentID, ItemID: s.assistantItemID, ContentType: contentType, Role: provider.MessageRoleAssistant, Stage: provider.RealtimeGenerationFinal})
}

func (s *geminiRealtimeSession) ensureInput() []provider.RealtimeEvent {
	if s.inputOpen {
		return nil
	}
	if s.inputItemID == "" {
		s.inputItemID = "item_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	s.inputContentID = "content_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	s.inputOpen = true
	return []provider.RealtimeEvent{{Type: provider.RealtimeEventContentStarted, ContentID: s.inputContentID, ItemID: s.inputItemID, ContentType: provider.RealtimeContentText, Role: provider.MessageRoleUser, Stage: provider.RealtimeGenerationFinal}}
}

func (s *geminiRealtimeSession) finishAssistant() []provider.RealtimeEvent {
	if !s.assistantOpen {
		return nil
	}
	s.assistantOpen = false
	return []provider.RealtimeEvent{{Type: provider.RealtimeEventContentDone, ResponseID: s.responseID, ContentID: s.assistantContentID, ItemID: s.assistantItemID, ContentType: s.assistantType}}
}

func (s *geminiRealtimeSession) finishInput() []provider.RealtimeEvent {
	if !s.inputOpen {
		return nil
	}
	s.inputOpen = false
	return []provider.RealtimeEvent{{Type: provider.RealtimeEventContentDone, ContentID: s.inputContentID, ItemID: s.inputItemID, ContentType: provider.RealtimeContentText}}
}

func (s *geminiRealtimeSession) resetResponse() {
	s.responseID = ""
	s.assistantItemID = ""
	s.assistantContentID = ""
	s.assistantType = ""
	s.assistantOpen = false
	s.interrupted = false
}
