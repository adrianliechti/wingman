package openai

import (
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
)

func TestTranscriptionStreamClosesOnEarlyExit(t *testing.T) {
	body := &responderLifecycleBody{
		Reader: strings.NewReader("data: {\"type\":\"transcript.text.delta\",\"delta\":\"hello\"}\n\n"),
	}
	client := &http.Client{Transport: responderLifecycleTransport(func(req *http.Request) (*http.Response, error) {
		req.Body.Close()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       body,
			Request:    req,
		}, nil
	})}
	transcriber, err := NewTranscriber("https://upstream.test/v1", "gpt-transcribe", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	input := provider.File{Name: "hello.wav", Content: []byte("audio"), ContentType: "audio/wav"}
	var text string
	for result, err := range transcriber.Transcribe(t.Context(), input, nil) {
		if err != nil {
			t.Fatal(err)
		}
		text = result.Text
		break
	}
	if text != "hello" || !body.closed {
		t.Fatalf("transcription text = %q, upstream body closed = %t", text, body.closed)
	}
}

func TestConvertTranscribeRequestLanguages(t *testing.T) {
	tests := []struct {
		model     string
		languages []string

		wantLanguage string
		wantList     []string
	}{
		{model: "gpt-transcribe", languages: []string{"en", "de"}, wantList: []string{"en", "de"}},
		{model: "gpt-transcribe", languages: []string{"en"}, wantLanguage: "en"},

		// every other model rejects the list, so it degrades to the first hint
		{model: "whisper-1", languages: []string{"en", "de"}, wantLanguage: "en"},
		{model: "gpt-4o-transcribe", languages: []string{"en", "de"}, wantLanguage: "en"},
		{model: "gpt-4o-transcribe-diarize", languages: []string{"en", "de"}, wantLanguage: "en"},

		{model: "whisper-1"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			transcriber := &Transcriber{Config: &Config{model: tt.model}}

			body, _, err := transcriber.convertTranscribeRequest(provider.File{Name: "a.wav"}, &provider.TranscribeOptions{
				Languages: tt.languages,
			})

			if err != nil {
				t.Fatalf("convertTranscribeRequest() error = %v", err)
			}

			if got := body.Language.Or(""); got != tt.wantLanguage {
				t.Errorf("language = %q, want %q", got, tt.wantLanguage)
			}

			if len(body.Languages) != len(tt.wantList) {
				t.Fatalf("languages = %v, want %v", body.Languages, tt.wantList)
			}

			for i, want := range tt.wantList {
				if body.Languages[i] != want {
					t.Errorf("languages[%d] = %q, want %q", i, body.Languages[i], want)
				}
			}
		})
	}
}

func TestConvertTranscribeRequestKeywords(t *testing.T) {
	for _, tt := range []struct {
		model string
		want  bool
	}{
		{model: "gpt-transcribe", want: true},
		{model: "gpt-live-transcribe", want: true},
		{model: "whisper-1"},
		{model: "gpt-4o-transcribe"},
		{model: "gpt-4o-transcribe-diarize"},
	} {
		t.Run(tt.model, func(t *testing.T) {
			transcriber := &Transcriber{Config: &Config{model: tt.model}}

			body, _, err := transcriber.convertTranscribeRequest(provider.File{Name: "a.wav"}, &provider.TranscribeOptions{
				Keywords: []string{"Wingman"},
			})

			if err != nil {
				t.Fatalf("convertTranscribeRequest() error = %v", err)
			}

			if got := len(body.Keywords) > 0; got != tt.want {
				t.Errorf("keywords forwarded = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConvertTranscribeRequestPrompt(t *testing.T) {
	for _, tt := range []struct {
		model string
		want  string
	}{
		{model: "whisper-1", want: "hello"},
		{model: "gpt-transcribe", want: "hello"},

		// diarization models reject prompt outright
		{model: "gpt-4o-transcribe-diarize"},
	} {
		t.Run(tt.model, func(t *testing.T) {
			transcriber := &Transcriber{Config: &Config{model: tt.model}}

			body, _, err := transcriber.convertTranscribeRequest(provider.File{Name: "a.wav"}, &provider.TranscribeOptions{
				Instructions: "hello",
			})

			if err != nil {
				t.Fatalf("convertTranscribeRequest() error = %v", err)
			}

			if got := body.Prompt.Or(""); got != tt.want {
				t.Errorf("prompt = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTranscriptionResponseFormat(t *testing.T) {
	for _, tt := range []struct {
		name string

		options provider.TranscribeOptions
		want    openai.AudioResponseFormat
	}{
		{name: "default", want: openai.AudioResponseFormatJSON},
		{name: "timestamps", options: provider.TranscribeOptions{Timestamps: true}, want: openai.AudioResponseFormatVerboseJSON},
		{name: "diarize", options: provider.TranscribeOptions{Diarize: true}, want: openai.AudioResponseFormatDiarizedJSON},
		{
			name:    "known speakers imply diarization",
			options: provider.TranscribeOptions{Speakers: []provider.TranscribeSpeaker{{Name: "a"}}},
			want:    openai.AudioResponseFormatDiarizedJSON,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := transcriptionResponseFormat(&tt.options); got != tt.want {
				t.Errorf("transcriptionResponseFormat() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSupportsTranscriptionStream(t *testing.T) {
	for _, tt := range []struct {
		model  string
		format openai.AudioResponseFormat
		want   bool
	}{
		{model: "gpt-transcribe", format: openai.AudioResponseFormatJSON, want: true},
		{model: "gpt-4o-transcribe-diarize", format: openai.AudioResponseFormatDiarizedJSON, want: true},

		{model: "whisper-1", format: openai.AudioResponseFormatJSON},
		{model: "gpt-transcribe", format: openai.AudioResponseFormatVerboseJSON},
		{model: "my-azure-deployment", format: openai.AudioResponseFormatJSON},
	} {
		t.Run(tt.model+"/"+string(tt.format), func(t *testing.T) {
			if got := supportsTranscriptionStream(tt.model, tt.format); got != tt.want {
				t.Errorf("supportsTranscriptionStream(%q, %q) = %v, want %v", tt.model, tt.format, got, tt.want)
			}
		})
	}
}
