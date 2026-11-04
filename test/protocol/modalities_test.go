package protocol_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json/v2"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

func modalityGateway(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	wire := httptest.NewServer(handler)
	t.Cleanup(wire.Close)
	cfg := &config.Config{Policy: noop.New()}
	options := []openai.Option{openai.WithToken("synthetic"), openai.WithMaxRetries(0)}
	embedder, err := openai.NewEmbedder(wire.URL, "text-embedding-3-small", options...)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RegisterEmbedder("embedding", embedder)
	renderer, err := openai.NewRenderer(wire.URL, "gpt-image-1", options...)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RegisterRenderer("image", renderer)
	synthesizer, err := openai.NewSynthesizer(wire.URL, "tts-1", options...)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RegisterSynthesizer("speech", synthesizer)
	transcriber, err := openai.NewTranscriber(wire.URL, "whisper-1", options...)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RegisterTranscriber("transcription", transcriber)
	return configuredGateway(t, cfg)
}

func TestEmbeddingWireContract(t *testing.T) {
	for _, format := range []string{"float", "base64"} {
		t.Run(format, func(t *testing.T) {
			local := modalityGateway(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/embeddings" {
					t.Errorf("upstream path = %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Error(err)
					return
				}
				if !reflect.DeepEqual(body["input"], []any{"first", "second"}) || body["dimensions"] != float64(2) || body["encoding_format"] != "float" {
					t.Errorf("lost embedding options: %v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"object":"list","model":"text-embedding-3-small","data":[{"object":"embedding","index":0,"embedding":[0.25,-0.5]},{"object":"embedding","index":1,"embedding":[1,0]}],"usage":{"prompt_tokens":3,"total_tokens":3}}`)
			})
			status, headers, raw := post(t, local, "/v1/embeddings", map[string]any{"model": "embedding", "input": []string{"first", "second"}, "dimensions": 2, "encoding_format": format})
			requireStatus(t, status, 200, raw)
			requireType(t, headers, "application/json")
			body := object(t, raw)
			equal(t, "object", body["object"], "list")
			equal(t, "tokens", at(body, "usage", "total_tokens"), float64(3))
			data, ok := body["data"].([]any)
			if !ok || len(data) != 2 {
				t.Fatalf("expected two embeddings: %s", raw)
			}
			for i, want := range [][]float32{{0.25, -0.5}, {1, 0}} {
				equal(t, "index", at(data[i], "index"), float64(i))
				equal(t, "embedding object", at(data[i], "object"), "embedding")
				var got []float32
				if format == "base64" {
					encoded, ok := at(data[i], "embedding").(string)
					if !ok {
						t.Fatalf("missing base64 vector: %v", data[i])
					}
					decoded, err := base64.StdEncoding.DecodeString(encoded)
					if err != nil {
						t.Fatal(err)
					}
					if len(decoded) != 8 {
						t.Fatalf("vector bytes = %d", len(decoded))
					}
					for j := 0; j < len(decoded); j += 4 {
						got = append(got, math.Float32frombits(binary.LittleEndian.Uint32(decoded[j:])))
					}
				} else {
					values, ok := at(data[i], "embedding").([]any)
					if !ok {
						t.Fatalf("missing float vector: %v", data[i])
					}
					for _, value := range values {
						got = append(got, float32(value.(float64)))
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("vector = %v, want %v", got, want)
				}
			}
		})
	}
}

func multipartBody(t *testing.T, fields map[string]string, fileField, filename string, data []byte) (string, *bytes.Buffer) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := w.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if fileField != "" {
		part, err := w.CreateFormFile(fileField, filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.FormDataContentType(), &body
}

func TestImageWireContract(t *testing.T) {
	image := []byte("\x89PNG\r\n\x1a\ncontract-image")
	for _, edit := range []bool{false, true} {
		for _, format := range []string{"b64_json", "url"} {
			t.Run(fmt.Sprintf("edit=%v/%s", edit, format), func(t *testing.T) {
				local := modalityGateway(t, func(w http.ResponseWriter, r *http.Request) {
					if edit {
						if r.URL.Path != "/images/edits" {
							t.Errorf("upstream path = %s", r.URL.Path)
						}
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							return
						}
						defer r.MultipartForm.RemoveAll()
						if r.FormValue("prompt") != "draw a kite" {
							t.Error("lost edit prompt")
						}
						file, _, err := r.FormFile("image[]")
						if err != nil {
							t.Error(err)
							return
						}
						defer file.Close()
						data, err := io.ReadAll(file)
						if err != nil {
							t.Error(err)
							return
						}
						if !bytes.Equal(data, image) {
							t.Error("image upload changed")
						}
					} else {
						if r.URL.Path != "/images/generations" {
							t.Errorf("upstream path = %s", r.URL.Path)
						}
						var body map[string]any
						if err := json.UnmarshalRead(r.Body, &body); err != nil {
							t.Error(err)
							return
						}
						if body["prompt"] != "draw a kite" {
							t.Error("lost generation prompt")
						}
					}
					w.Header().Set("Content-Type", "application/json")
					json.MarshalWrite(w, map[string]any{"created": 1, "data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(image)}}})
				})
				var status int
				var headers http.Header
				var raw []byte
				if edit {
					ct, body := multipartBody(t, map[string]string{"model": "image", "prompt": "draw a kite", "response_format": format}, "image", "kite.png", image)
					status, headers, raw = request(t, local, http.MethodPost, "/v1/images/edits", ct, body)
				} else {
					status, headers, raw = post(t, local, "/v1/images/generations", map[string]any{"model": "image", "prompt": "draw a kite", "response_format": format})
				}
				requireStatus(t, status, 200, raw)
				requireType(t, headers, "application/json")
				body := object(t, raw)
				key, want := "b64_json", base64.StdEncoding.EncodeToString(image)
				if format == "url" {
					key, want = "url", "data:image/png;base64,"+want
				}
				equal(t, "image data", at(body, "data", 0, key), want)
			})
		}
	}
}

func TestSpeechWireContract(t *testing.T) {
	const audio = "RIFF\x00\x01\x02\xffWAVEcontract"
	for _, format := range []string{"audio", "sse"} {
		t.Run(format, func(t *testing.T) {
			local := modalityGateway(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/audio/speech" {
					t.Errorf("upstream path = %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Error(err)
					return
				}
				if body["input"] != "hello" || body["voice"] != "alloy" || body["response_format"] != "wav" {
					t.Errorf("lost speech options: %v", body)
				}
				w.Header().Set("Content-Type", "audio/wav")
				io.WriteString(w, audio[:8])
				http.NewResponseController(w).Flush()
				io.WriteString(w, audio[8:])
			})
			status, headers, raw := post(t, local, "/v1/audio/speech", map[string]any{"model": "speech", "input": "hello", "voice": "alloy", "response_format": "wav", "stream_format": format})
			requireStatus(t, status, 200, raw)
			if format == "audio" {
				requireType(t, headers, "audio/wav")
				equal(t, "audio bytes", string(raw), audio)
				return
			}
			requireType(t, headers, "text/event-stream")
			var decoded []byte
			done := 0
			for _, e := range frames(t, raw) {
				if e.Data["type"] == "speech.audio.delta" {
					chunk, err := base64.StdEncoding.DecodeString(e.Data["audio"].(string))
					if err != nil {
						t.Fatal(err)
					}
					decoded = append(decoded, chunk...)
				}
				if e.Data["type"] == "speech.audio.done" {
					done++
				}
			}
			equal(t, "audio bytes", string(decoded), audio)
			equal(t, "terminal count", done, 1)
		})
	}
}

func TestTranscriptionWireContract(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			local := modalityGateway(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/audio/transcriptions" {
					t.Errorf("upstream path = %s", r.URL.Path)
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("language") != "de" || r.FormValue("prompt") != "Swiss German" {
					t.Error("lost transcription options")
				}
				file, _, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
					return
				}
				defer file.Close()
				data, err := io.ReadAll(file)
				if err != nil {
					t.Error(err)
					return
				}
				if string(data) != "RIFFcontract-audio" {
					t.Error("audio upload changed")
				}
				w.Header().Set("Content-Type", "application/json")
				json.MarshalWrite(w, map[string]any{"text": answer})
			})
			ct, body := multipartBody(t, map[string]string{"model": "transcription", "language": "de", "prompt": "Swiss German", "response_format": format}, "file", "audio.wav", []byte("RIFFcontract-audio"))
			status, headers, raw := request(t, local, http.MethodPost, "/v1/audio/transcriptions", ct, body)
			requireStatus(t, status, 200, raw)
			if format == "text" {
				requireType(t, headers, "text/plain")
				equal(t, "text", string(raw), answer)
			} else {
				requireType(t, headers, "application/json")
				equal(t, "text", object(t, raw)["text"], answer)
			}
		})
	}
}

func TestInvalidModalityInputsStayLocal(t *testing.T) {
	var calls atomic.Int32
	local := modalityGateway(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected upstream call", 500)
	})
	for _, tc := range []struct{ path, body string }{
		{"/v1/embeddings", `{"model":"embedding"}`},
		{"/v1/embeddings", `{"model":"embedding","input":null}`},
		{"/v1/embeddings", `{"model":123,"input":"hello"}`},
		{"/v1/embeddings", `{"model":"embedding","input":{"text":"hello"}}`},
		{"/v1/embeddings", `{"model":"embedding","input":[]}`},
		{"/v1/embeddings", `{"model":"embedding","input":["hello",5]}`},
		{"/v1/audio/speech", `{"model":"speech","input":"hello","stream_format":"invalid"}`},
		{"/v1/images/generations", `{"model":`},
	} {
		t.Run(tc.path+"/"+tc.body, func(t *testing.T) {
			status, _, raw := request(t, local, http.MethodPost, tc.path, "application/json", strings.NewReader(tc.body))
			requireStatus(t, status, 400, raw)
			if at(object(t, raw), "error", "message") == nil {
				t.Fatalf("missing error: %s", raw)
			}
		})
	}
	for _, tc := range []struct{ path, model string }{{"/v1/images/edits", "image"}, {"/v1/audio/transcriptions", "transcription"}} {
		t.Run(tc.path+"/missing_file", func(t *testing.T) {
			ct, body := multipartBody(t, map[string]string{"model": tc.model}, "", "", nil)
			status, _, raw := request(t, local, http.MethodPost, tc.path, ct, body)
			requireStatus(t, status, 400, raw)
		})
	}
	equal(t, "upstream requests", calls.Load(), int32(0))
}

func TestModalityUpstreamErrors(t *testing.T) {
	for _, tc := range []struct{ path, model, fileField, filename string }{
		{"/v1/embeddings", "embedding", "", ""},
		{"/v1/images/generations", "image", "", ""},
		{"/v1/images/edits", "image", "image", "image.png"},
		{"/v1/audio/speech", "speech", "", ""},
		{"/v1/audio/transcriptions", "transcription", "file", "audio.wav"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			var calls atomic.Int32
			local := modalityGateway(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(429)
				io.WriteString(w, `{"error":{"message":"contract limit","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
			})
			var status int
			var headers http.Header
			var raw []byte
			if tc.fileField != "" {
				ct, body := multipartBody(t, map[string]string{"model": tc.model, "prompt": "kite"}, tc.fileField, tc.filename, []byte("\x89PNG\r\n\x1a\ncontract"))
				status, headers, raw = request(t, local, http.MethodPost, tc.path, ct, body)
			} else {
				status, headers, raw = post(t, local, tc.path, map[string]any{"model": tc.model, "input": "hello", "prompt": "kite", "voice": "alloy"})
			}
			requireStatus(t, status, 429, raw)
			requireType(t, headers, "application/json")
			equal(t, "Retry-After", headers.Get("Retry-After"), "2")
			equal(t, "upstream requests", calls.Load(), int32(1))
			message, _ := at(object(t, raw), "error", "message").(string)
			if !strings.Contains(message, "contract limit") {
				t.Fatalf("lost upstream error: %s", raw)
			}
		})
	}
}
