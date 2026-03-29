package azurespeech

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

var _ provider.Synthesizer = (*Synthesizer)(nil)

type Synthesizer struct {
	*Config
}

func NewSynthesizer(region, model string, options ...Option) (*Synthesizer, error) {
	if region == "" {
		return nil, fmt.Errorf("azure speech region is required (e.g. eastus)")
	}

	cfg := &Config{
		region: region,
		model:  model,

		client: http.DefaultClient,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Synthesizer{
		Config: cfg,
	}, nil
}

func (s *Synthesizer) Synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) (*provider.Synthesis, error) {
	if options == nil {
		options = new(provider.SynthesizeOptions)
	}

	voice := mapVoice(options.Voice)

	outputFormat := mapOutputFormat(options.Format)
	contentType := mapContentType(options.Format)

	ssml := buildSSML(voice, content)

	endpoint := s.ttsURL() + "/cognitiveservices/v1"

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(ssml))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", outputFormat)

	if s.token != "" {
		req.Header.Set("Ocp-Apim-Subscription-Key", s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &provider.Synthesis{
		ID:    uuid.NewString(),
		Model: s.model,

		Content:     data,
		ContentType: contentType,
	}, nil
}

// voiceMap maps OpenAI voice names to Azure multilingual voices.
// Turbo multilingual voices are direct Azure equivalents of OpenAI voices.
// Multilingual voices auto-detect the language of the input text.
var voiceMap = map[string]string{
	"alloy":   "en-US-AlloyTurboMultilingualNeural",
	"ash":     "en-US-AndrewMultilingualNeural",
	"ballad":  "en-US-BrianMultilingualNeural",
	"coral":   "en-US-EmmaMultilingualNeural",
	"echo":    "en-US-EchoTurboMultilingualNeural",
	"fable":   "en-US-FableTurboMultilingualNeural",
	"nova":    "en-US-NovaTurboMultilingualNeural",
	"onyx":    "en-US-OnyxTurboMultilingualNeural",
	"sage":    "en-US-AndrewMultilingualNeural",
	"shimmer": "en-US-ShimmerTurboMultilingualNeural",
}

func mapVoice(voice string) string {
	if voice == "" {
		return "en-US-AvaMultilingualNeural"
	}

	if mapped, ok := voiceMap[strings.ToLower(voice)]; ok {
		return mapped
	}

	// Already an Azure voice name — use as-is
	return voice
}

func buildSSML(voice, text string) string {
	// Extract lang from voice name (e.g. "en-US-JennyNeural" -> "en-US")
	lang := "en-US"
	parts := strings.SplitN(voice, "-", 3)
	if len(parts) >= 2 {
		lang = parts[0] + "-" + parts[1]
	}

	return fmt.Sprintf(
		`<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='%s'><voice name='%s'>%s</voice></speak>`,
		lang, voice, text,
	)
}

// mapOutputFormat maps OpenAI response_format values to Azure X-Microsoft-OutputFormat values.
// OpenAI formats: mp3, opus, aac, flac, wav, pcm
func mapOutputFormat(format string) string {
	switch strings.ToLower(format) {
	case "mp3":
		return "audio-24khz-160kbitrate-mono-mp3"
	case "opus":
		return "ogg-24khz-16bit-mono-opus"
	case "aac":
		return "audio-24khz-160kbitrate-mono-mp3"
	case "flac":
		return "riff-24khz-16bit-mono-pcm"
	case "wav":
		return "riff-24khz-16bit-mono-pcm"
	case "pcm":
		return "raw-24khz-16bit-mono-pcm"
	default:
		return "audio-24khz-160kbitrate-mono-mp3"
	}
}

// mapContentType maps OpenAI response_format values to HTTP Content-Type values.
func mapContentType(format string) string {
	switch strings.ToLower(format) {
	case "mp3":
		return "audio/mpeg"
	case "opus":
		return "audio/opus"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	case "wav":
		return "audio/wav"
	case "pcm":
		return "audio/pcm"
	default:
		return "audio/mpeg"
	}
}
