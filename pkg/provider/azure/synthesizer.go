package azure

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

var _ provider.Synthesizer = (*Synthesizer)(nil)

type Synthesizer struct {
	*Config
}

func NewSynthesizer(endpoint, model string, options ...Option) (*Synthesizer, error) {
	cfg, err := newConfig(endpoint, model, options...)
	if err != nil {
		return nil, err
	}

	return &Synthesizer{
		Config: cfg,
	}, nil
}

// Synthesize streams the audio as Azure produces it. Compressed and raw
// formats arrive incrementally; wav (RIFF) is buffered by Azure until done.
func (s *Synthesizer) Synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) iter.Seq2[*provider.Synthesis, error] {
	return func(yield func(*provider.Synthesis, error) bool) {
		resp, contentType, err := s.synthesize(ctx, content, options)

		if err != nil {
			yield(nil, err)
			return
		}

		defer resp.Body.Close()

		id := uuid.NewString()

		buffer := make([]byte, 32*1024)

		for {
			n, err := resp.Body.Read(buffer)

			if n > 0 {
				chunk := provider.Synthesis{
					ID:    id,
					Model: s.model,

					Content:     bytes.Clone(buffer[:n]),
					ContentType: contentType,
				}

				if !yield(&chunk, nil) {
					return
				}
			}

			if err != nil {
				if err != io.EOF {
					yield(nil, err)
				}

				return
			}
		}
	}
}

func (s *Synthesizer) synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) (*http.Response, string, error) {
	if options == nil {
		options = new(provider.SynthesizeOptions)
	}

	voice := options.Voice
	if voice == "" {
		voice = s.voice
	}
	voice = mapVoice(voice)

	outputFormat, contentType, err := mapFormat(options.Format)
	if err != nil {
		return nil, "", err
	}

	ssml, err := buildSSML(voice, content, options.Speed)
	if err != nil {
		return nil, "", err
	}

	endpoint := s.url + "/tts/cognitiveservices/v1"

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(ssml))
	if err != nil {
		return nil, "", err
	}

	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", outputFormat)
	req.Header.Set("User-Agent", "wingman")

	if s.token != "" {
		req.Header.Set("Ocp-Apim-Subscription-Key", s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()

		err := convertError(resp)

		// Azure answers unknown voices, including HD voices that aren't offered
		// in the resource's region, with an empty 400.
		var perr *provider.ProviderError
		if errors.As(err, &perr) && perr.Code == http.StatusBadRequest && perr.Message == http.StatusText(http.StatusBadRequest) {
			perr.Message = fmt.Sprintf("azure speech rejected the request; check that voice %q is available in the resource's region", voice)
		}

		return nil, "", err
	}

	return resp, contentType, nil
}

// voiceMap maps OpenAI voice names to Azure Dragon HD voices. The speech
// voices are matched on measured pitch, pitch range, and pace (fable keeps its
// British accent); the realtime voices on language, accent, and presentation.
// Locales without a Dragon HD persona use Dragon HD Omni.
var voiceMap = map[string]string{
	// Speech API voices
	"alloy":   "en-US-Alloy:DragonHDLatestNeural",
	"ash":     "en-US-Brian:DragonHDLatestNeural",
	"ballad":  "en-US-Jimmie:DragonHDLatestNeural",
	"cedar":   "en-US-Steffan:DragonHDLatestNeural",
	"coral":   "en-US-Jenny:DragonHDLatestNeural",
	"echo":    "en-US-Davis:DragonHDLatestNeural",
	"fable":   "en-GB-Ollie:DragonHDLatestNeural",
	"marin":   "en-US-Emma:DragonHDLatestNeural",
	"nova":    "en-US-Nova:DragonHDLatestNeural",
	"onyx":    "en-US-Andrew:DragonHDLatestNeural",
	"sage":    "en-US-Tiana:DragonHDLatestNeural",
	"shimmer": "en-US-Serena:DragonHDLatestNeural",
	"verse":   "en-US-Tyler:DragonHDLatestNeural",

	// Realtime voices
	"beacon":   "fil-PH-Angelo:DragonHDLatestNeural",
	"bossa":    "pt-BR-Thalita:DragonHDLatestNeural",
	"brise":    "fr-FR-Vivienne:DragonHDLatestNeural",
	"cinder":   "en-US-Vance:DragonHDLatestNeural",
	"delta":    "en-US-Phoebe:DragonHDLatestNeural",
	"flitz":    "de-DE-Florian:DragonHDLatestNeural",
	"gleam":    "en-US-Ava:DragonHDLatestNeural",
	"harema":   "ja-JP-Nanami:DragonHDLatestNeural",
	"juni":     "de-DE-Katja:DragonHDOmniLatestNeural",
	"meridian": "en-US-Adam:DragonHDLatestNeural",
	"nira":     "en-IN-Aarti:DragonHDLatestNeural",
	"noeul":    "ko-KR-SunHi:DragonHDLatestNeural",
	"nuri":     "de-DE-Seraphina:DragonHDLatestNeural",
	"quartz":   "en-AU-Natasha:DragonHDOmniLatestNeural",
	"ripple":   "en-AU-William:DragonHDOmniLatestNeural",
	"shida":    "ja-JP-Masaru:DragonHDLatestNeural",
	"sillage":  "fr-FR-Remy:DragonHDLatestNeural",
	"stone":    "en-IE-Connor:DragonHDOmniLatestNeural",
	"tempo":    "pt-BR-Macerio:DragonHDLatestNeural",
	"vesper":   "en-GB-Ryan:DragonHDLatestNeural",
	"willow":   "en-IE-Emily:DragonHDOmniLatestNeural",
}

func mapVoice(voice string) string {
	if voice == "" {
		return defaultVoice
	}

	if mapped, ok := voiceMap[strings.ToLower(voice)]; ok {
		return mapped
	}

	// Already an Azure voice name — use as-is
	return voice
}

type ssmlProsody struct {
	Rate string `xml:"rate,attr"`
	Text string `xml:",chardata"`
}

func buildSSML(voice, text string, speed *float32) (string, error) {
	// Extract lang from voice name (e.g. "en-US-JennyNeural" -> "en-US")
	lang := "en-US"
	parts := strings.SplitN(voice, "-", 3)
	if len(parts) >= 2 {
		lang = parts[0] + "-" + parts[1]
	}

	document := struct {
		XMLName xml.Name `xml:"speak"`
		Version string   `xml:"version,attr"`
		XMLNS   string   `xml:"xmlns,attr"`
		Lang    string   `xml:"xml:lang,attr"`
		Voice   struct {
			Name    string       `xml:"name,attr"`
			Text    string       `xml:",chardata"`
			Prosody *ssmlProsody `xml:"prosody,omitempty"`
		} `xml:"voice"`
	}{
		Version: "1.0",
		XMLNS:   "http://www.w3.org/2001/10/synthesis",
		Lang:    lang,
	}
	document.Voice.Name = voice

	// Rate is a multiplier of the voice's natural pace; Azure clamps it to 0.5–2.
	if speed != nil && *speed > 0 && *speed != 1 {
		document.Voice.Prosody = &ssmlProsody{
			Rate: strconv.FormatFloat(float64(*speed), 'f', -1, 32),
			Text: text,
		}
	} else {
		document.Voice.Text = text
	}

	data, err := xml.Marshal(document)
	return string(data), err
}

// mapFormat keeps the Azure output encoding and returned MIME type consistent.
func mapFormat(format string) (string, string, error) {
	switch strings.ToLower(format) {
	case "", "mp3":
		return "audio-24khz-160kbitrate-mono-mp3", "audio/mpeg", nil
	case "opus":
		return "ogg-24khz-16bit-mono-opus", "audio/opus", nil
	case "wav":
		return "riff-24khz-16bit-mono-pcm", "audio/wav", nil
	case "pcm":
		return "raw-24khz-16bit-mono-pcm", "audio/pcm", nil
	default:
		return "", "", provider.InvalidRequest(fmt.Errorf("azure speech does not support response_format %q; use mp3, opus, wav, or pcm", format))
	}
}
