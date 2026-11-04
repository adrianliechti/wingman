package chat_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server/openai/chat"

	"github.com/go-chi/chi/v5"
)

// TestChatInputAudioRoundtrip wires the OpenAI-compatible chat handler to the
// pkg/provider/openai Completer pointed at a fake upstream OpenAI server. It
// verifies that an `input_audio` part on the request reaches the upstream as a
// proper input_audio content part, end-to-end through both packages.
func TestChatInputAudioRoundtrip(t *testing.T) {
	const (
		audioFormat = "wav"
		modelName   = "audio-test-model"
		replyText   = "I hear audio."
	)

	rawAudio := []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x41, 0x56, 0x45}
	encodedAudio := base64.StdEncoding.EncodeToString(rawAudio)

	var (
		mu                sync.Mutex
		upstreamReqBodies [][]byte
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}

		mu.Lock()
		upstreamReqBodies = append(upstreamReqBodies, body)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		writeChunk := func(chunk map[string]any) {
			b, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
		}

		writeChunk(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   modelName,
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]any{"role": "assistant", "content": replyText},
				},
			},
		})

		writeChunk(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   modelName,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         map[string]any{},
					"finish_reason": "stop",
				},
			},
		})

		writeChunk(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   modelName,
			"choices": []map[string]any{},
			"usage": map[string]any{
				"prompt_tokens":     5,
				"completion_tokens": 3,
				"total_tokens":      8,
			},
		})

		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	completer, err := openai.NewCompleter(upstream.URL+"/v1/", modelName, openai.WithToken("test-token"))
	if err != nil {
		t.Fatalf("new completer: %v", err)
	}

	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(modelName, completer)

	handler := chat.New(cfg)
	router := chi.NewRouter()
	handler.Attach(router)

	wingmanSrv := httptest.NewServer(router)
	defer wingmanSrv.Close()

	reqBody := map[string]any{
		"model": modelName,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "What is in this recording?"},
					{
						"type": "input_audio",
						"input_audio": map[string]any{
							"data":   encodedAudio,
							"format": audioFormat,
						},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(reqBody)

	resp, err := http.Post(wingmanSrv.URL+"/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("wingman status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Role    string `json:"role"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		t.Fatalf("decode response: %v\n%s", err, respBody)
	}

	if len(result.Choices) != 1 || result.Choices[0].Message.Content != replyText {
		t.Fatalf("unexpected response content: %+v\n%s", result, respBody)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(upstreamReqBodies) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(upstreamReqBodies))
	}

	// Decode the upstream payload and walk to the input_audio part.
	var upstream1 struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []jsontext.Value `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(upstreamReqBodies[0], &upstream1); err != nil {
		t.Fatalf("decode upstream body: %v\n%s", err, upstreamReqBodies[0])
	}

	if len(upstream1.Messages) != 1 || upstream1.Messages[0].Role != "user" {
		t.Fatalf("expected one user message upstream, got %+v", upstream1.Messages)
	}

	var foundAudio bool
	for _, part := range upstream1.Messages[0].Content {
		var probe struct {
			Type       string `json:"type"`
			InputAudio *struct {
				Data   string `json:"data"`
				Format string `json:"format"`
			} `json:"input_audio"`
		}
		if err := json.Unmarshal(part, &probe); err != nil {
			t.Fatalf("decode content part: %v\n%s", err, part)
		}

		if probe.Type != "input_audio" {
			continue
		}
		if probe.InputAudio == nil {
			t.Fatalf("input_audio part has no input_audio field: %s", part)
		}
		if probe.InputAudio.Format != audioFormat {
			t.Fatalf("expected format %q, got %q", audioFormat, probe.InputAudio.Format)
		}
		if probe.InputAudio.Data != encodedAudio {
			t.Fatalf("audio base64 roundtrip mismatch")
		}
		// Round-trip the base64 to make sure decoding still works upstream-side.
		decoded, err := base64.StdEncoding.DecodeString(probe.InputAudio.Data)
		if err != nil {
			t.Fatalf("upstream audio data not valid base64: %v", err)
		}
		if !bytes.Equal(decoded, rawAudio) {
			t.Fatalf("upstream audio bytes differ from input")
		}
		foundAudio = true
	}

	if !foundAudio {
		t.Fatalf("upstream request did not contain input_audio part:\n%s", upstreamReqBodies[0])
	}
}
