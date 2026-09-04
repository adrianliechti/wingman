package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Realtime opens a stateful, bidirectional audio session with a provider.
// Protocol handlers translate their wire events to and from this interface.
type Realtime interface {
	Defaults() RealtimeOptions
	Capabilities() RealtimeCapabilities
	Connect(ctx context.Context, options *RealtimeOptions) (RealtimeSession, error)
}

// RealtimeSession is a live provider session. Implementations must allow Events
// to be consumed while input methods are being called.
type RealtimeSession interface {
	Update(ctx context.Context, options *RealtimeOptions) error

	SendAudio(ctx context.Context, audio []byte) error
	CommitAudio(ctx context.Context) error
	ClearAudio(ctx context.Context) error
	SendMessage(ctx context.Context, message Message) error
	SendToolResult(ctx context.Context, id, output string) error
	TruncateOutput(ctx context.Context, itemID string, audioEnd time.Duration) error

	Respond(ctx context.Context, options *RealtimeResponseOptions) error
	Interrupt(ctx context.Context) error

	Events() <-chan RealtimeEvent
	Close() error
}

// RealtimeCapabilities describes operations whose native behavior differs
// between stateful providers. Wire adapters can use it to avoid inventing
// acknowledgements that a provider will emit itself.
type RealtimeCapabilities struct {
	SessionUpdates      bool
	AudioBufferClearing bool
	ResponseRequests    bool
	Interruption        bool
	InputActivityEvents bool
}

var ErrRealtimeUnsupported = errors.New("realtime operation is unsupported")

func UnsupportedRealtimeOperation(operation string) error {
	return fmt.Errorf("%w: %s", ErrRealtimeUnsupported, operation)
}

type RealtimeOptions struct {
	Instructions string
	Voice        string

	InputAudio  RealtimeAudioFormat
	OutputAudio RealtimeAudioFormat

	MaxTokens   *int
	Temperature *float32
	TopP        *float32

	Tools      []Tool
	ToolChoice ToolChoice

	OutputModalities    []RealtimeModality
	TurnDetection       *RealtimeTurnDetection
	InputTranscription  *RealtimeTranscription
	InputNoiseReduction RealtimeNoiseReduction
	OutputTranscription bool
	Truncation          *RealtimeTruncation

	// History is sent before interactive input starts. Providers whose native
	// protocol has a dedicated history phase can therefore preserve ordering.
	History []Message
}

type RealtimeResponseOptions struct {
	OutputModalities []RealtimeModality
}

type RealtimeModality string

const (
	RealtimeModalityText  RealtimeModality = "text"
	RealtimeModalityAudio RealtimeModality = "audio"
)

type RealtimeTurnDetection struct {
	Type              RealtimeTurnDetectionType
	Eagerness         string
	Threshold         *float32
	PrefixPadding     time.Duration
	SilenceDuration   time.Duration
	IdleTimeout       time.Duration
	CreateResponse    bool
	InterruptResponse bool
}

type RealtimeTurnDetectionType string

const (
	RealtimeTurnDetectionServer   RealtimeTurnDetectionType = "server_vad"
	RealtimeTurnDetectionSemantic RealtimeTurnDetectionType = "semantic_vad"
)

type RealtimeTranscription struct {
	Model    string
	Language string
	Prompt   string
}

type RealtimeNoiseReduction string

const (
	RealtimeNoiseReductionNear RealtimeNoiseReduction = "near_field"
	RealtimeNoiseReductionFar  RealtimeNoiseReduction = "far_field"
)

type RealtimeTruncation struct {
	Disabled              bool
	RetentionRatio        *float32
	PostInstructionTokens *int
}

type RealtimeAudioFormat struct {
	Encoding   RealtimeAudioEncoding
	SampleRate int
	SampleSize int
	Channels   int
}

type RealtimeAudioEncoding string

const (
	RealtimeAudioPCM  RealtimeAudioEncoding = "pcm"
	RealtimeAudioPCMU RealtimeAudioEncoding = "pcmu"
	RealtimeAudioPCMA RealtimeAudioEncoding = "pcma"
)

type RealtimeEventType string

const (
	RealtimeEventResponseStarted    RealtimeEventType = "response_started"
	RealtimeEventContentStarted     RealtimeEventType = "content_started"
	RealtimeEventTextDelta          RealtimeEventType = "text_delta"
	RealtimeEventAudioDelta         RealtimeEventType = "audio_delta"
	RealtimeEventToolCall           RealtimeEventType = "tool_call"
	RealtimeEventContentDone        RealtimeEventType = "content_done"
	RealtimeEventResponseDone       RealtimeEventType = "response_done"
	RealtimeEventUsage              RealtimeEventType = "usage"
	RealtimeEventInterrupted        RealtimeEventType = "interrupted"
	RealtimeEventInputSpeechStarted RealtimeEventType = "input_speech_started"
	RealtimeEventInputSpeechStopped RealtimeEventType = "input_speech_stopped"
	RealtimeEventInputCommitted     RealtimeEventType = "input_committed"
	RealtimeEventInputCleared       RealtimeEventType = "input_cleared"
	// RealtimeEventInputTranscriptionPreview carries a replaceable, speculative
	// transcript snapshot. It is deliberately distinct from TextDelta: providers
	// such as Gemini revise these hypotheses, so treating them as append-only
	// text would corrupt the final transcript.
	RealtimeEventInputTranscriptionPreview RealtimeEventType = "input_transcription_preview"
	RealtimeEventInputTranscriptionFailed  RealtimeEventType = "input_transcription_failed"
	RealtimeEventInputTranscriptionSegment RealtimeEventType = "input_transcription_segment"
	RealtimeEventInputTimeoutTriggered     RealtimeEventType = "input_timeout_triggered"
	RealtimeEventRateLimits                RealtimeEventType = "rate_limits"
	RealtimeEventError                     RealtimeEventType = "error"
)

type RealtimeContentType string

const (
	RealtimeContentText  RealtimeContentType = "text"
	RealtimeContentAudio RealtimeContentType = "audio"
	RealtimeContentTool  RealtimeContentType = "tool"
)

type RealtimeGenerationStage string

const (
	RealtimeGenerationFinal       RealtimeGenerationStage = "final"
	RealtimeGenerationSpeculative RealtimeGenerationStage = "speculative"
)

type RealtimeEvent struct {
	Type RealtimeEventType

	ResponseID     string
	ContentID      string
	ItemID         string
	PreviousItemID string

	ContentType RealtimeContentType
	Role        MessageRole
	Stage       RealtimeGenerationStage

	Text  string
	Audio []byte

	ToolCall   *ToolCall
	Usage      *RealtimeUsage
	RateLimits []RealtimeRateLimit

	AudioStart time.Duration
	AudioEnd   time.Duration

	ContentIndex int
	SegmentID    string
	Speaker      string
	SegmentStart float64
	SegmentEnd   float64

	StopReason string

	// Error contains provider-agnostic, client-safe metadata for error events.
	// Err retains the diagnostic upstream error and may contain request IDs or
	// other details that should be logged rather than sent over the wire.
	Error *RealtimeError
	Err   error
}

// RealtimeError is the normalized error contract shared by realtime
// providers and protocol adapters. Type and Code follow the target wire
// protocol when the upstream exposes them; Message is safe to show to users.
type RealtimeError struct {
	Type    string
	Code    string
	Message string
	Param   string
	EventID string
}

type RealtimeRateLimit struct {
	Name       string
	Limit      int
	Remaining  int
	ResetAfter time.Duration
}

type RealtimeUsage struct {
	InputTokens  int
	OutputTokens int

	InputTextTokens  int
	InputAudioTokens int

	OutputTextTokens  int
	OutputAudioTokens int
}
