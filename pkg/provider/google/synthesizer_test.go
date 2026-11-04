package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// rewriteTransport sends every request to the test server, whatever host the
// genai client dialed.
type rewriteTransport struct {
	target *url.URL
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	clone.Host = t.target.Host

	return http.DefaultTransport.RoundTrip(clone)
}

// newTestClient serves the handler from a local server and returns a client
// that reaches it regardless of the URL the genai client builds.
func newTestClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	return &http.Client{Transport: rewriteTransport{target: target}}
}

func newTestSynthesizer(t *testing.T, handler http.HandlerFunc) *Synthesizer {
	t.Helper()

	s, err := NewSynthesizer("gemini-3.8-flash-tts", WithToken("test-token"), WithClient(newTestClient(t, handler)))
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func sseAudio(mimeType string, data []byte) string {
	body := map[string]any{
		"responseId": "resp_123",
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role": "model",
					"parts": []any{
						map[string]any{
							"inlineData": map[string]any{
								"mimeType": mimeType,
								"data":     base64.StdEncoding.EncodeToString(data),
							},
						},
					},
				},
			},
		},
	}

	raw, _ := json.Marshal(body)

	return "data: " + string(raw) + "\n\n"
}

func serveSSE(chunks ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		for _, chunk := range chunks {
			io.WriteString(w, chunk)
		}
	}
}

func collect(t *testing.T, s *Synthesizer, options *provider.SynthesizeOptions) ([]*provider.Synthesis, error) {
	t.Helper()

	var chunks []*provider.Synthesis

	for chunk, err := range s.Synthesize(context.Background(), "Have a wonderful day!", options) {
		if err != nil {
			return chunks, err
		}

		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

func TestSynthesize_RequestShape(t *testing.T) {
	var (
		gotPath  string
		gotQuery url.Values
		gotKey   string
		gotBody  map[string]any
	)

	s := newTestSynthesizer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		gotKey = r.Header.Get("x-goog-api-key")
		json.UnmarshalRead(r.Body, &gotBody)

		serveSSE(sseAudio("audio/l16; rate=24000; channels=1", []byte{1, 0, 2, 0}))(w, r)
	})

	if _, err := collect(t, s, &provider.SynthesizeOptions{Voice: "alloy", Format: "pcm"}); err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(gotPath, "/models/gemini-3.8-flash-tts:streamGenerateContent") {
		t.Errorf("expected a streamGenerateContent call, got path %q", gotPath)
	}

	if gotQuery.Get("alt") != "sse" {
		t.Errorf("expected alt=sse, got query %v", gotQuery)
	}

	if gotKey != "test-token" {
		t.Errorf("expected the token as x-goog-api-key, got %q", gotKey)
	}

	config, _ := gotBody["generationConfig"].(map[string]any)

	if modalities, _ := config["responseModalities"].([]any); len(modalities) != 1 || modalities[0] != "AUDIO" {
		t.Errorf("expected responseModalities [AUDIO], got %v", config["responseModalities"])
	}

	speech, _ := config["speechConfig"].(map[string]any)
	voice, _ := speech["voiceConfig"].(map[string]any)
	prebuilt, _ := voice["prebuiltVoiceConfig"].(map[string]any)

	if prebuilt["voiceName"] != "Kore" {
		t.Errorf("expected alloy to map to Kore, got %v", prebuilt["voiceName"])
	}

	contents, _ := gotBody["contents"].([]any)

	if len(contents) != 1 {
		t.Fatalf("expected one content, got %v", gotBody["contents"])
	}

	content, _ := contents[0].(map[string]any)
	parts, _ := content["parts"].([]any)
	part, _ := parts[0].(map[string]any)

	if content["role"] != "user" || part["text"] != "Have a wonderful day!" {
		t.Errorf("expected the input as a user text part, got %v", content)
	}
}

func TestSynthesize_PCMStreamsChunks(t *testing.T) {
	first := []byte{1, 0, 2, 0, 3, 0}
	second := []byte{4, 0, 5, 0}

	s := newTestSynthesizer(t, serveSSE(
		sseAudio("audio/l16; rate=24000; channels=1", first),
		sseAudio("audio/l16; rate=24000; channels=1", second),
	))

	chunks, err := collect(t, s, &provider.SynthesizeOptions{Format: "pcm"})
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks) != 2 {
		t.Fatalf("expected the two chunks to be forwarded, got %d", len(chunks))
	}

	for i, want := range [][]byte{first, second} {
		if chunks[i].ContentType != "audio/pcm" {
			t.Errorf("chunk %d: expected audio/pcm, got %q", i, chunks[i].ContentType)
		}

		if !bytes.Equal(chunks[i].Content, want) {
			t.Errorf("chunk %d: expected samples %v, got %v", i, want, chunks[i].Content)
		}

		if chunks[i].ID != "resp_123" || chunks[i].Model != "gemini-3.8-flash-tts" {
			t.Errorf("chunk %d: expected response id and model, got %+v", i, chunks[i])
		}
	}
}

// Newer models lead the stream with a chunk that carries a WAV header; the
// header must be dropped from a pcm stream.
func TestSynthesize_PCMStripsLeadingHeader(t *testing.T) {
	first := []byte{1, 0, 2, 0}
	second := []byte{3, 0, 4, 0}

	s := newTestSynthesizer(t, serveSSE(
		sseAudio("audio/wav", encodeWAV(first, defaultAudioFormat)),
		sseAudio("audio/l16; rate=24000; channels=1", second),
	))

	chunks, err := collect(t, s, &provider.SynthesizeOptions{Format: "pcm"})
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks) != 2 || !bytes.Equal(chunks[0].Content, first) || !bytes.Equal(chunks[1].Content, second) {
		t.Fatalf("expected raw samples %v and %v, got %+v", first, second, chunks)
	}
}

func TestSynthesize_CollectsWAV(t *testing.T) {
	first := []byte{1, 0, 2, 0}
	second := []byte{3, 0, 4, 0, 5, 0}

	for _, format := range []string{"", "wav", "mp3", "opus"} {
		t.Run("format="+format, func(t *testing.T) {
			s := newTestSynthesizer(t, serveSSE(
				sseAudio("audio/wav", encodeWAV(first, defaultAudioFormat)),
				sseAudio("audio/l16; rate=24000; channels=1", second),
			))

			chunks, err := collect(t, s, &provider.SynthesizeOptions{Format: format})
			if err != nil {
				t.Fatal(err)
			}

			if len(chunks) != 1 {
				t.Fatalf("expected a single WAV result, got %d chunks", len(chunks))
			}

			if chunks[0].ContentType != "audio/wav" {
				t.Errorf("expected audio/wav, got %q", chunks[0].ContentType)
			}

			samples, wavFormat, ok := parseWAV(chunks[0].Content)

			if !ok {
				t.Fatalf("expected a RIFF/WAVE payload, got %v", chunks[0].Content[:12])
			}

			if want := append(append([]byte{}, first...), second...); !bytes.Equal(samples, want) {
				t.Errorf("expected samples %v, got %v", want, samples)
			}

			if wavFormat != defaultAudioFormat {
				t.Errorf("expected 24 kHz mono 16-bit, got %+v", wavFormat)
			}
		})
	}
}

func TestSynthesize_APIError(t *testing.T) {
	s := newTestSynthesizer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"code":400,"message":"No matching speaker voice found","status":"INVALID_ARGUMENT"}}`)
	})

	_, err := collect(t, s, nil)

	var providerErr *provider.ProviderError

	if !errors.As(err, &providerErr) {
		t.Fatalf("expected a provider error, got %v", err)
	}

	if providerErr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", providerErr.Code)
	}

	if !strings.Contains(providerErr.Message, "No matching speaker voice") {
		t.Errorf("expected the API message, got %q", providerErr.Message)
	}
}

func TestSynthesize_NoAudio(t *testing.T) {
	s := newTestSynthesizer(t, serveSSE(
		`data: {"responseId":"resp_123","candidates":[{"content":{"role":"model","parts":[{"text":"I cannot do that."}]}}]}`+"\n\n",
	))

	chunks, err := collect(t, s, nil)

	if err == nil || len(chunks) != 0 {
		t.Fatalf("expected an error and no chunks, got %v and %d chunks", err, len(chunks))
	}
}

func TestParseWAV_ClampsDeclaredSize(t *testing.T) {
	samples := []byte{1, 0, 2, 0, 3, 0, 4, 0}

	// A streamed header announcing more data than the chunk carries.
	data := encodeWAV(samples, defaultAudioFormat)[:44+4]

	got, format, ok := parseWAV(data)

	if !ok {
		t.Fatal("expected a WAV payload to be recognized")
	}

	if !bytes.Equal(got, samples[:4]) {
		t.Errorf("expected the delivered samples %v, got %v", samples[:4], got)
	}

	if format != defaultAudioFormat {
		t.Errorf("expected the header format, got %+v", format)
	}
}

func TestEncodeWAV_Header(t *testing.T) {
	samples := []byte{1, 0, 2, 0}

	format := audioFormat{sampleRate: 16000, channels: 2, bitsPerSample: 16}

	data := encodeWAV(samples, format)

	if len(data) != 44+len(samples) {
		t.Fatalf("expected a 44-byte header, got %d bytes total", len(data))
	}

	got, gotFormat, ok := parseWAV(data)

	if !ok || !bytes.Equal(got, samples) || gotFormat != format {
		t.Errorf("expected the header to round-trip, got ok=%v samples=%v format=%+v", ok, got, gotFormat)
	}

	if !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WAVE")) {
		t.Errorf("expected a RIFF/WAVE header, got %q", data[:12])
	}
}

func TestParseAudioMIME(t *testing.T) {
	tests := []struct {
		mimeType string
		want     audioFormat
	}{
		{"audio/L16;codec=pcm;rate=24000", defaultAudioFormat},
		{"audio/l16; rate=16000; channels=2", audioFormat{sampleRate: 16000, channels: 2, bitsPerSample: 16}},
		{"", defaultAudioFormat},
		{"audio/pcm", defaultAudioFormat},
	}

	for _, tt := range tests {
		if got := parseAudioMIME(tt.mimeType); got != tt.want {
			t.Errorf("parseAudioMIME(%q) = %+v, want %+v", tt.mimeType, got, tt.want)
		}
	}
}

func TestMapVoice(t *testing.T) {
	tests := []struct {
		voice string
		want  string
	}{
		{"", "Kore"},
		{"alloy", "Kore"},
		{"Nova", "Aoede"},
		{"kore", "Kore"},
		{"ZEPHYR", "Zephyr"},
		{"voice_abc123", "voice_abc123"},
	}

	for _, tt := range tests {
		if got := mapVoice(tt.voice); got != tt.want {
			t.Errorf("mapVoice(%q) = %q, want %q", tt.voice, got, tt.want)
		}
	}
}
