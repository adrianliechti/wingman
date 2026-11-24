package audio

import (
	"context"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"sync"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

// SampleURL is the spoken-audio fixture from the OpenAI documentation.
const SampleURL = "https://cdn.openai.com/API/docs/audio/alloy.wav"

const Path = "/audio/transcriptions"

// Model pairs the model id configured in wingman with the upstream OpenAI
// model the response is compared against.
type Model struct {
	Wingman   string
	Reference string
}

func TranscribeModel() Model {
	return model("TEST_OPENAI_TRANSCRIBE_MODEL", "gpt-transcribe")
}

func DiarizeModel() Model {
	return model("TEST_OPENAI_DIARIZE_MODEL", "gpt-4o-transcribe-diarize")
}

func WhisperModel() Model {
	return model("TEST_OPENAI_WHISPER_MODEL", "whisper-1")
}

func SpeechModel() Model {
	return model("TEST_OPENAI_SPEECH_MODEL", "gpt-4o-mini-tts")
}

func model(key, fallback string) Model {
	name := fallback

	if v := os.Getenv(key); v != "" {
		name = v
	}

	reference := name

	if v := os.Getenv(key + "_REFERENCE"); v != "" {
		reference = v
	}

	return Model{Wingman: name, Reference: reference}
}

var (
	sampleOnce sync.Once
	sampleData []byte
	sampleErr  error
)

// Sample fetches the shared audio fixture once per test binary.
func Sample(t *testing.T) []byte {
	t.Helper()

	sampleOnce.Do(func() {
		req, err := http.NewRequest(http.MethodGet, SampleURL, nil)

		if err != nil {
			sampleErr = err
			return
		}

		resp, err := http.DefaultClient.Do(req)

		if err != nil {
			sampleErr = err
			return
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Skipf("audio fixture returned status %d", resp.StatusCode)
			return
		}

		sampleData, sampleErr = io.ReadAll(resp.Body)
	})

	if sampleErr != nil {
		t.Skipf("audio fixture unavailable: %v", sampleErr)
	}

	if len(sampleData) == 0 {
		t.Skip("audio fixture is empty")
	}

	return sampleData
}

func files(t *testing.T) []harness.MultipartFile {
	t.Helper()

	return []harness.MultipartFile{
		{
			Name:        "file",
			Filename:    "sample.wav",
			ContentType: "audio/wav",
			Content:     Sample(t),
		},
	}
}

// WithModel returns fields with the model field set to name.
func WithModel(fields []harness.MultipartField, name string) []harness.MultipartField {
	result := []harness.MultipartField{{Name: "model", Value: name}}

	for _, field := range fields {
		if field.Name == "model" {
			continue
		}

		result = append(result, field)
	}

	return result
}

// CompareHTTP transcribes the fixture through OpenAI and wingman and returns
// both responses.
func CompareHTTP(t *testing.T, h *openai.Harness, m Model, fields []harness.MultipartField) (*harness.RawResponse, *harness.RawResponse) {
	t.Helper()
	h.RequireConfigured(t, m.Wingman)

	ctx := context.Background()
	parts := files(t)

	openaiResp, err := h.Client.PostMultipart(ctx, h.OpenAI, Path, WithModel(fields, m.Reference), parts)

	if err != nil {
		t.Fatalf("openai request failed: %v", err)
	}

	wingmanResp, err := h.Client.PostMultipart(ctx, h.Wingman, Path, WithModel(fields, m.Wingman), parts)

	if err != nil {
		t.Fatalf("wingman request failed: %v", err)
	}

	if openaiResp.StatusCode != http.StatusOK {
		t.Skipf("openai returned status %d for %s: %s", openaiResp.StatusCode, m.Reference, string(openaiResp.RawBody))
	}

	if wingmanResp.StatusCode != http.StatusOK {
		t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}

	return openaiResp, wingmanResp
}

// Post transcribes the fixture through a single endpoint without asserting the
// status, for cases where the two endpoints deliberately disagree.
func Post(t *testing.T, h *openai.Harness, ep harness.Endpoint, name string, fields []harness.MultipartField) *harness.RawResponse {
	t.Helper()

	resp, err := h.Client.PostMultipart(context.Background(), ep, Path, WithModel(fields, name), files(t))

	if err != nil {
		t.Fatalf("%s request failed: %v", ep.Name, err)
	}

	return resp
}

// CompareSSE transcribes the fixture with stream=true through both endpoints.
func CompareSSE(t *testing.T, h *openai.Harness, m Model, fields []harness.MultipartField) ([]*harness.SSEEvent, []*harness.SSEEvent) {
	t.Helper()
	h.RequireConfigured(t, m.Wingman)

	ctx := context.Background()
	parts := files(t)

	fields = append(slices.Clone(fields), harness.MultipartField{Name: "stream", Value: "true"})

	openaiEvents, err := h.Client.PostMultipartSSE(ctx, h.OpenAI, Path, WithModel(fields, m.Reference), parts)

	if err != nil {
		t.Skipf("openai stream request failed: %v", err)
	}

	wingmanEvents, err := h.Client.PostMultipartSSE(ctx, h.Wingman, Path, WithModel(fields, m.Wingman), parts)

	if err != nil {
		t.Fatalf("wingman stream request failed: %v", err)
	}

	return openaiEvents, wingmanEvents
}

// AssertElementKeys checks that array elements carry the same field names in
// both responses. Element counts and values legitimately differ between runs,
// so only the key sets are compared.
func AssertElementKeys(t *testing.T, label string, expected, actual any, ignore ...string) {
	t.Helper()

	expectedKeys := elementKeys(expected, ignore)
	actualKeys := elementKeys(actual, ignore)

	if len(expectedKeys) == 0 {
		t.Skipf("%s: openai returned no elements to compare", label)
	}

	if len(actualKeys) == 0 {
		t.Errorf("%s: wingman returned no elements, openai keys %v", label, expectedKeys)
		return
	}

	for _, key := range expectedKeys {
		if !slices.Contains(actualKeys, key) {
			t.Errorf("%s: field %q present in openai but missing in wingman (wingman keys %v)", label, key, actualKeys)
		}
	}

	for _, key := range actualKeys {
		if !slices.Contains(expectedKeys, key) {
			t.Errorf("%s: field %q present in wingman but missing in openai (openai keys %v)", label, key, expectedKeys)
		}
	}
}

func elementKeys(value any, ignore []string) []string {
	items, ok := value.([]any)

	if !ok {
		return nil
	}

	seen := map[string]bool{}

	for _, item := range items {
		object, ok := item.(map[string]any)

		if !ok {
			continue
		}

		for key := range object {
			if slices.Contains(ignore, key) {
				continue
			}

			seen[key] = true
		}
	}

	keys := make([]string, 0, len(seen))

	for key := range seen {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

// EventTypes returns the distinct event types in order of first appearance.
func EventTypes(events []*harness.SSEEvent) []string {
	var types []string

	for _, event := range events {
		if event.Raw == "[DONE]" {
			if !slices.Contains(types, "[DONE]") {
				types = append(types, "[DONE]")
			}

			continue
		}

		name, _ := event.Data["type"].(string)

		if name == "" || slices.Contains(types, name) {
			continue
		}

		types = append(types, name)
	}

	return types
}
