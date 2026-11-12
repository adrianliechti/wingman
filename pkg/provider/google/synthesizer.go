package google

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"iter"
	"mime"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
	"google.golang.org/genai"
)

var _ provider.Synthesizer = (*Synthesizer)(nil)

type Synthesizer struct {
	*Config
}

func NewSynthesizer(model string, options ...Option) (*Synthesizer, error) {
	cfg := &Config{
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Synthesizer{
		Config: cfg,
	}, nil
}

// Synthesize runs a Gemini TTS model through streamGenerateContent with an
// AUDIO response modality. That path only produces uncompressed audio:
// headerless 16-bit PCM chunks, on newer models led by a chunk carrying a WAV
// header. A pcm request forwards the samples as they arrive; every other
// format is collected into a single WAV result, since mp3, opus, aac and flac
// are not available. Instructions and speed have no counterpart on this API
// (system instructions are rejected by TTS models) and are ignored.
func (s *Synthesizer) Synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) iter.Seq2[*provider.Synthesis, error] {
	return func(yield func(*provider.Synthesis, error) bool) {
		if options == nil {
			options = new(provider.SynthesizeOptions)
		}

		client, err := s.newClient(ctx)

		if err != nil {
			yield(nil, err)
			return
		}

		contents := []*genai.Content{
			genai.NewContentFromText(content, genai.RoleUser),
		}

		config := &genai.GenerateContentConfig{
			ResponseModalities: []string{string(genai.ModalityAudio)},

			SpeechConfig: &genai.SpeechConfig{
				VoiceConfig: &genai.VoiceConfig{
					PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{
						VoiceName: mapVoice(options.Voice),
					},
				},
			},

			Store: new(false),
		}

		streaming := strings.EqualFold(options.Format, "pcm")

		id := uuid.NewString()

		format := defaultAudioFormat
		received := false

		var samples bytes.Buffer

		for resp, err := range client.Models.GenerateContentStream(ctx, s.model, contents, config) {
			if err != nil {
				yield(nil, convertError(err))
				return
			}

			if resp.ResponseID != "" {
				id = resp.ResponseID
			}

			for _, blob := range audioBlobs(resp) {
				data, blobFormat := decodePCM(blob)

				if len(data) == 0 {
					continue
				}

				format = blobFormat
				received = true

				if !streaming {
					samples.Write(data)
					continue
				}

				chunk := &provider.Synthesis{
					ID:    id,
					Model: s.model,

					Content:     data,
					ContentType: "audio/pcm",
				}

				if !yield(chunk, nil) {
					return
				}
			}
		}

		if !received {
			yield(nil, errors.New("gemini: no audio in response"))
			return
		}

		if streaming {
			return
		}

		yield(&provider.Synthesis{
			ID:    id,
			Model: s.model,

			Content:     encodeWAV(samples.Bytes(), format),
			ContentType: "audio/wav",
		}, nil)
	}
}

func audioBlobs(resp *genai.GenerateContentResponse) []*genai.Blob {
	var blobs []*genai.Blob

	for _, candidate := range resp.Candidates {
		if candidate == nil || candidate.Content == nil {
			continue
		}

		for _, part := range candidate.Content.Parts {
			if part == nil || part.InlineData == nil {
				continue
			}

			blobs = append(blobs, part.InlineData)
		}
	}

	return blobs
}

// audioFormat describes linear PCM samples.
type audioFormat struct {
	sampleRate    int
	channels      int
	bitsPerSample int
}

// defaultAudioFormat is what Gemini TTS models emit: 24 kHz, mono, 16-bit.
var defaultAudioFormat = audioFormat{
	sampleRate:    24000,
	channels:      1,
	bitsPerSample: 16,
}

// decodePCM returns the raw samples of an audio blob. A blob that carries a
// WAV header is unwrapped and described by that header; a headerless blob is
// described by its MIME parameters (audio/L16;codec=pcm;rate=24000 or
// audio/l16; rate=24000; channels=1).
func decodePCM(blob *genai.Blob) ([]byte, audioFormat) {
	if data, format, ok := parseWAV(blob.Data); ok {
		return data, format
	}

	return blob.Data, parseAudioMIME(blob.MIMEType)
}

func parseAudioMIME(mimeType string) audioFormat {
	format := defaultAudioFormat

	_, params, err := mime.ParseMediaType(mimeType)

	if err != nil {
		return format
	}

	if rate, err := strconv.Atoi(params["rate"]); err == nil && rate > 0 {
		format.sampleRate = rate
	}

	if channels, err := strconv.Atoi(params["channels"]); err == nil && channels > 0 {
		format.channels = channels
	}

	return format
}

// parseWAV extracts the samples and format of a RIFF/WAVE payload. A declared
// data size larger than the payload is clamped, since a streamed header may
// announce the whole utterance while only carrying its first samples.
func parseWAV(data []byte) ([]byte, audioFormat, bool) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, audioFormat{}, false
	}

	format := defaultAudioFormat

	offset := 12

	for offset+8 <= len(data) {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))

		body := data[offset+8:]

		if size > len(body) {
			size = len(body)
		}

		switch id {
		case "fmt ":
			if size >= 16 {
				format.channels = int(binary.LittleEndian.Uint16(body[2:4]))
				format.sampleRate = int(binary.LittleEndian.Uint32(body[4:8]))
				format.bitsPerSample = int(binary.LittleEndian.Uint16(body[14:16]))
			}

		case "data":
			return body[:size], format, true
		}

		offset += 8 + size + size%2
	}

	return nil, format, true
}

// encodeWAV wraps linear PCM samples in a canonical 44-byte RIFF/WAVE header.
func encodeWAV(samples []byte, format audioFormat) []byte {
	blockAlign := format.channels * format.bitsPerSample / 8
	byteRate := format.sampleRate * blockAlign

	header := make([]byte, 0, 44)

	header = append(header, "RIFF"...)
	header = binary.LittleEndian.AppendUint32(header, uint32(36+len(samples)))
	header = append(header, "WAVE"...)

	header = append(header, "fmt "...)
	header = binary.LittleEndian.AppendUint32(header, 16)
	header = binary.LittleEndian.AppendUint16(header, 1)
	header = binary.LittleEndian.AppendUint16(header, uint16(format.channels))
	header = binary.LittleEndian.AppendUint32(header, uint32(format.sampleRate))
	header = binary.LittleEndian.AppendUint32(header, uint32(byteRate))
	header = binary.LittleEndian.AppendUint16(header, uint16(blockAlign))
	header = binary.LittleEndian.AppendUint16(header, uint16(format.bitsPerSample))

	header = append(header, "data"...)
	header = binary.LittleEndian.AppendUint32(header, uint32(len(samples)))

	return append(header, samples...)
}

// voiceMap maps OpenAI voice names to Gemini prebuilt voices of similar
// character.
var voiceMap = map[string]string{
	"alloy":   "Kore",
	"ash":     "Charon",
	"ballad":  "Orus",
	"coral":   "Sulafat",
	"echo":    "Iapetus",
	"fable":   "Puck",
	"nova":    "Aoede",
	"onyx":    "Algenib",
	"sage":    "Vindemiatrix",
	"shimmer": "Zephyr",
	"verse":   "Fenrir",
	"marin":   "Callirrhoe",
	"cedar":   "Achird",
}

// prebuiltVoices lists the Gemini studio voices, in their canonical spelling.
var prebuiltVoices = []string{
	"Zephyr", "Puck", "Charon", "Kore", "Fenrir", "Leda", "Orus", "Aoede",
	"Callirrhoe", "Autonoe", "Enceladus", "Iapetus", "Umbriel", "Algieba",
	"Despina", "Erinome", "Algenib", "Rasalgethi", "Laomedeia", "Achernar",
	"Alnilam", "Schedar", "Gacrux", "Pulcherrima", "Achird", "Zubenelgenubi",
	"Vindemiatrix", "Sadachbia", "Sadaltager", "Sulafat",
}

func mapVoice(voice string) string {
	if voice == "" {
		return "Kore"
	}

	if mapped, ok := voiceMap[strings.ToLower(voice)]; ok {
		return mapped
	}

	for _, name := range prebuiltVoices {
		if strings.EqualFold(name, voice) {
			return name
		}
	}

	// Voice library, designed or replicated voice IDs — use as-is
	return voice
}
