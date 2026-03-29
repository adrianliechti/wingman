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

func NewSynthesizer(url, model string, options ...Option) (*Synthesizer, error) {
	if url == "" {
		return nil, fmt.Errorf("azure speech url is required (e.g. https://eastus.tts.speech.microsoft.com)")
	}

	url = strings.TrimRight(url, "/")

	cfg := &Config{
		url:   url,
		model: model,

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

	language := options.Language
	if language == "" {
		language = detectLanguage(options.Instructions)
	}

	voice := mapVoice(options.Voice, language)

	outputFormat := mapOutputFormat(options.Format)
	contentType := mapContentType(options.Format)

	ssml := buildSSML(voice, content)

	endpoint := s.url + "/cognitiveservices/v1"

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

// voiceMap maps OpenAI voice names to Azure Speech voice names per locale.
// The empty string key is the default (en-US) fallback.
var voiceMap = map[string]map[string]string{
	"alloy": {
		"":      "en-US-AvaNeural",
		"de-DE": "de-DE-AmalaNeural",
		"fr-FR": "fr-FR-DeniseNeural",
		"es-ES": "es-ES-ElviraNeural",
		"it-IT": "it-IT-ElsaNeural",
		"pt-BR": "pt-BR-FranciscaNeural",
		"ja-JP": "ja-JP-NanamiNeural",
		"ko-KR": "ko-KR-SunHiNeural",
		"zh-CN": "zh-CN-XiaoxiaoNeural",
	},
	"ash": {
		"":      "en-US-AndrewNeural",
		"de-DE": "de-DE-ConradNeural",
		"fr-FR": "fr-FR-HenriNeural",
		"es-ES": "es-ES-AlvaroNeural",
		"it-IT": "it-IT-DiegoNeural",
		"pt-BR": "pt-BR-AntonioNeural",
		"ja-JP": "ja-JP-KeitaNeural",
		"ko-KR": "ko-KR-InJoonNeural",
		"zh-CN": "zh-CN-YunxiNeural",
	},
	"ballad": {
		"":      "en-US-BrianNeural",
		"de-DE": "de-DE-FlorianMultilingualNeural",
		"fr-FR": "fr-FR-RemyMultilingualNeural",
		"es-ES": "es-ES-AlvaroNeural",
		"it-IT": "it-IT-GiuseppeNeural",
		"pt-BR": "pt-BR-AntonioNeural",
		"ja-JP": "ja-JP-KeitaNeural",
		"ko-KR": "ko-KR-InJoonNeural",
		"zh-CN": "zh-CN-YunyangNeural",
	},
	"coral": {
		"":      "en-US-JennyNeural",
		"de-DE": "de-DE-KatjaNeural",
		"fr-FR": "fr-FR-DeniseNeural",
		"es-ES": "es-ES-ElviraNeural",
		"it-IT": "it-IT-IsabellaNeural",
		"pt-BR": "pt-BR-FranciscaNeural",
		"ja-JP": "ja-JP-NanamiNeural",
		"ko-KR": "ko-KR-SunHiNeural",
		"zh-CN": "zh-CN-XiaoxiaoNeural",
	},
	"echo": {
		"":      "en-US-ChristopherNeural",
		"de-DE": "de-DE-ConradNeural",
		"fr-FR": "fr-FR-HenriNeural",
		"es-ES": "es-ES-AlvaroNeural",
		"it-IT": "it-IT-DiegoNeural",
		"pt-BR": "pt-BR-AntonioNeural",
		"ja-JP": "ja-JP-KeitaNeural",
		"ko-KR": "ko-KR-InJoonNeural",
		"zh-CN": "zh-CN-YunxiNeural",
	},
	"fable": {
		"":      "en-US-AriaNeural",
		"de-DE": "de-DE-SeraphinaMultilingualNeural",
		"fr-FR": "fr-FR-VivienneMultilingualNeural",
		"es-ES": "es-ES-ElviraNeural",
		"it-IT": "it-IT-ElsaNeural",
		"pt-BR": "pt-BR-FranciscaNeural",
		"ja-JP": "ja-JP-NanamiNeural",
		"ko-KR": "ko-KR-SunHiNeural",
		"zh-CN": "zh-CN-XiaomoNeural",
	},
	"nova": {
		"":      "en-US-EmmaNeural",
		"de-DE": "de-DE-AmalaNeural",
		"fr-FR": "fr-FR-DeniseNeural",
		"es-ES": "es-ES-ElviraNeural",
		"it-IT": "it-IT-IsabellaNeural",
		"pt-BR": "pt-BR-FranciscaNeural",
		"ja-JP": "ja-JP-NanamiNeural",
		"ko-KR": "ko-KR-SunHiNeural",
		"zh-CN": "zh-CN-XiaohanNeural",
	},
	"onyx": {
		"":      "en-US-DavisNeural",
		"de-DE": "de-DE-ConradNeural",
		"fr-FR": "fr-FR-HenriNeural",
		"es-ES": "es-ES-AlvaroNeural",
		"it-IT": "it-IT-DiegoNeural",
		"pt-BR": "pt-BR-AntonioNeural",
		"ja-JP": "ja-JP-KeitaNeural",
		"ko-KR": "ko-KR-InJoonNeural",
		"zh-CN": "zh-CN-YunjianNeural",
	},
	"sage": {
		"":      "en-US-GuyNeural",
		"de-DE": "de-DE-ConradNeural",
		"fr-FR": "fr-FR-HenriNeural",
		"es-ES": "es-ES-AlvaroNeural",
		"it-IT": "it-IT-GiuseppeNeural",
		"pt-BR": "pt-BR-AntonioNeural",
		"ja-JP": "ja-JP-KeitaNeural",
		"ko-KR": "ko-KR-InJoonNeural",
		"zh-CN": "zh-CN-YunyangNeural",
	},
	"shimmer": {
		"":      "en-US-AvaMultilingualNeural",
		"de-DE": "de-DE-SeraphinaMultilingualNeural",
		"fr-FR": "fr-FR-VivienneMultilingualNeural",
		"es-ES": "es-ES-ElviraNeural",
		"it-IT": "it-IT-ElsaNeural",
		"pt-BR": "pt-BR-FranciscaNeural",
		"ja-JP": "ja-JP-NanamiNeural",
		"ko-KR": "ko-KR-SunHiNeural",
		"zh-CN": "zh-CN-XiaoxiaoNeural",
	},
}

// defaultVoices maps a locale to a sensible default voice when no voice is specified.
var defaultVoices = map[string]string{
	"de-DE": "de-DE-AmalaNeural",
	"fr-FR": "fr-FR-DeniseNeural",
	"es-ES": "es-ES-ElviraNeural",
	"it-IT": "it-IT-IsabellaNeural",
	"pt-BR": "pt-BR-FranciscaNeural",
	"ja-JP": "ja-JP-NanamiNeural",
	"ko-KR": "ko-KR-SunHiNeural",
	"zh-CN": "zh-CN-XiaoxiaoNeural",
}

// languageMap maps language names found in instructions to Azure locale codes.
var languageMap = map[string]string{
	"english":    "en-US",
	"german":     "de-DE",
	"french":     "fr-FR",
	"spanish":    "es-ES",
	"italian":    "it-IT",
	"portuguese": "pt-BR",
	"japanese":   "ja-JP",
	"korean":     "ko-KR",
	"chinese":    "zh-CN",
}

func detectLanguage(instructions string) string {
	lower := strings.ToLower(instructions)

	for name, locale := range languageMap {
		if strings.Contains(lower, name) {
			return locale
		}
	}

	return ""
}

// shortLocaleMap maps short language codes to full Azure locale codes.
var shortLocaleMap = map[string]string{
	"en": "en-US",
	"de": "de-DE",
	"fr": "fr-FR",
	"es": "es-ES",
	"it": "it-IT",
	"pt": "pt-BR",
	"ja": "ja-JP",
	"ko": "ko-KR",
	"zh": "zh-CN",
}

func normalizeLocale(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))

	if locale, ok := shortLocaleMap[language]; ok {
		return locale
	}

	return language
}

func mapVoice(voice, language string) string {
	language = normalizeLocale(language)

	// No voice specified — pick a default for the language
	if voice == "" {
		if language != "" {
			if v, ok := defaultVoices[language]; ok {
				return v
			}
		}

		return "en-US-AvaNeural"
	}

	// Check if this is an OpenAI voice name
	key := strings.ToLower(voice)
	if locales, ok := voiceMap[key]; ok {
		if language != "" {
			if v, ok := locales[language]; ok {
				return v
			}
		}

		return locales[""]
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
