package azure

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSpeechRequiresResourceURL(t *testing.T) {
	for _, endpoint := range []string{"", "eastus", "ftp://speech.test", "https://speech.test/tts", "https://speech.test/?key=secret", "https://speech.test/#fragment", "https://user:secret@speech.test", "https://%"} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := NewSynthesizer(endpoint, "tts"); err == nil {
				t.Fatal("synthesizer accepted an invalid resource URL")
			}
			if _, err := NewTranscriber(endpoint, "stt"); err == nil {
				t.Fatal("transcriber accepted an invalid resource URL")
			}
		})
	}
}

func TestSynthesizeResourceRequest(t *testing.T) {
	const input = `Grüezi & hello <world>! "Quotes" and 'apostrophes'. </voice><voice name="injected">`
	for _, tt := range []struct {
		name         string
		defaultVoice string
		options      *provider.SynthesizeOptions
		voice        string
		lang         string
		rate         string
		outputFormat string
		contentType  string
	}{
		{
			name: "default HD voice", voice: "en-US-Ava:DragonHDLatestNeural", lang: "en-US",
			outputFormat: "audio-24khz-160kbitrate-mono-mp3", contentType: "audio/mpeg",
		},
		{
			name: "configured voice", defaultVoice: "de-DE-Seraphina:DragonHDLatestNeural",
			options: &provider.SynthesizeOptions{Format: "wav"}, voice: "de-DE-Seraphina:DragonHDLatestNeural", lang: "de-DE",
			outputFormat: "riff-24khz-16bit-mono-pcm", contentType: "audio/wav",
		},
		{
			name: "request overrides configured voice", defaultVoice: "en-US-Ava:DragonHDLatestNeural",
			options: &provider.SynthesizeOptions{Voice: "fr-FR-Vivienne:DragonHDLatestNeural", Format: "pcm"},
			voice:   "fr-FR-Vivienne:DragonHDLatestNeural", lang: "fr-FR",
			outputFormat: "raw-24khz-16bit-mono-pcm", contentType: "audio/pcm",
		},
		{
			name: "OpenAI alias uses HD", options: &provider.SynthesizeOptions{Voice: "ALLOY", Format: "OPUS"},
			voice: "en-US-Alloy:DragonHDLatestNeural", lang: "en-US",
			outputFormat: "ogg-24khz-16bit-mono-opus", contentType: "audio/opus",
		},
		{
			name: "Omni voice passes through", options: &provider.SynthesizeOptions{Voice: "de-DE-Conrad:DragonHDOmniLatestNeural"},
			voice: "de-DE-Conrad:DragonHDOmniLatestNeural", lang: "de-DE",
			outputFormat: "audio-24khz-160kbitrate-mono-mp3", contentType: "audio/mpeg",
		},
		{
			name: "speed sets prosody rate", options: &provider.SynthesizeOptions{Voice: "nova", Speed: new(float32(1.5))},
			voice: "en-US-Nova:DragonHDLatestNeural", lang: "en-US", rate: "1.5",
			outputFormat: "audio-24khz-160kbitrate-mono-mp3", contentType: "audio/mpeg",
		},
		{
			name: "default speed adds no prosody", options: &provider.SynthesizeOptions{Speed: new(float32(1))},
			voice: "en-US-Ava:DragonHDLatestNeural", lang: "en-US",
			outputFormat: "audio-24khz-160kbitrate-mono-mp3", contentType: "audio/mpeg",
		},
		{
			name: "voice attribute escaped", options: &provider.SynthesizeOptions{Voice: `en-US-Voice'"/><break/><voice name="other`},
			voice: `en-US-Voice'"/><break/><voice name="other`, lang: "en-US",
			outputFormat: "audio-24khz-160kbitrate-mono-mp3", contentType: "audio/mpeg",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodPost || req.URL.String() != "https://speech.test/tts/cognitiveservices/v1" {
					t.Errorf("request = %s %s", req.Method, req.URL)
				}
				if req.Header.Get("Ocp-Apim-Subscription-Key") != "test-key" || req.Header.Get("Content-Type") != "application/ssml+xml" || req.Header.Get("User-Agent") == "" {
					t.Error("missing synthesis request headers")
				}
				if got := req.Header.Get("X-Microsoft-OutputFormat"); got != tt.outputFormat {
					t.Errorf("output format = %s, want %s", got, tt.outputFormat)
				}
				var document struct {
					XMLName xml.Name `json:"xmlName"`
					Lang    string   `xml:"http://www.w3.org/XML/1998/namespace lang,attr"`
					Voices  []struct {
						Name    string `xml:"name,attr"`
						Text    string `xml:",chardata"`
						Prosody []struct {
							Rate string `xml:"rate,attr"`
							Text string `xml:",chardata"`
						} `xml:"prosody"`
					} `xml:"voice"`
				}
				if err := xml.NewDecoder(req.Body).Decode(&document); err != nil {
					t.Fatalf("invalid SSML: %v", err)
				}
				if document.XMLName.Space != "http://www.w3.org/2001/10/synthesis" || document.XMLName.Local != "speak" || document.Lang != tt.lang {
					t.Errorf("unexpected SSML root: %+v", document)
				}
				if len(document.Voices) != 1 || document.Voices[0].Name != tt.voice {
					t.Fatalf("SSML did not preserve voice: %+v", document.Voices)
				}
				if voice := document.Voices[0]; tt.rate == "" {
					if voice.Text != input || len(voice.Prosody) != 0 {
						t.Errorf("SSML did not preserve plain text: %+v", voice)
					}
				} else if voice.Text != "" || len(voice.Prosody) != 1 || voice.Prosody[0].Rate != tt.rate || voice.Prosody[0].Text != input {
					t.Errorf("SSML did not wrap text in prosody rate %s: %+v", tt.rate, voice)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("audio bytes"))}, nil
			})}
			synthesizer, err := NewSynthesizer("https://speech.test/", "azure-tts", WithToken("test-key"), WithVoice(tt.defaultVoice), WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			results := 0
			for result, err := range synthesizer.Synthesize(context.Background(), input, tt.options) {
				if err != nil {
					t.Fatal(err)
				}
				results++
				if result.ID == "" || result.Model != "azure-tts" || result.ContentType != tt.contentType || string(result.Content) != "audio bytes" {
					t.Errorf("unexpected synthesis: %+v", result)
				}
			}
			if calls != 1 || results != 1 {
				t.Fatalf("requests = %d, results = %d", calls, results)
			}
		})
	}
}

func TestSynthesizeStreamsChunks(t *testing.T) {
	audio := bytes.Repeat([]byte("0123456789"), 10_000)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(audio))}, nil
	})}
	synthesizer, err := NewSynthesizer("https://speech.test", "azure-tts", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	var content []byte
	ids := map[string]bool{}
	chunks := 0
	for result, err := range synthesizer.Synthesize(context.Background(), "Hello", &provider.SynthesizeOptions{Format: "pcm"}) {
		if err != nil {
			t.Fatal(err)
		}
		chunks++
		ids[result.ID] = true
		if result.ContentType != "audio/pcm" || result.Model != "azure-tts" {
			t.Errorf("unexpected chunk: %+v", result)
		}
		content = append(content, result.Content...)
	}
	if chunks < 2 || len(ids) != 1 || !bytes.Equal(content, audio) {
		t.Fatalf("chunks = %d, ids = %d, bytes = %d; want several chunks of one synthesis with all %d bytes", chunks, len(ids), len(content), len(audio))
	}
}

func TestSynthesizeExplainsEmptyBadRequest(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	synthesizer, err := NewSynthesizer("https://speech.test", "azure-tts", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	for result, err := range synthesizer.Synthesize(context.Background(), "Hello", &provider.SynthesizeOptions{Voice: "en-US-Nobody:DragonHDLatestNeural"}) {
		if result != nil || provider.CodeFromError(err, 0) != http.StatusBadRequest || !strings.Contains(err.Error(), `"en-US-Nobody:DragonHDLatestNeural" is available`) {
			t.Fatalf("result = %v, error = %v; want a 400 naming the voice", result, err)
		}
	}
}

func TestOpenAIVoicesMapToDistinctHDVoices(t *testing.T) {
	voices := []string{
		"alloy", "ash", "ballad", "cedar", "coral", "echo", "fable", "marin", "nova", "onyx", "sage", "shimmer", "verse",
		"beacon", "bossa", "brise", "cinder", "delta", "flitz", "gleam", "harema", "juni", "meridian", "nira", "noeul",
		"nuri", "quartz", "ripple", "shida", "sillage", "stone", "tempo", "vesper", "willow",
	}
	seen := map[string]string{}
	for _, voice := range voices {
		mapped := mapVoice(voice)
		if !strings.Contains(mapped, ":DragonHD") {
			t.Errorf("%s maps to %q, want a Dragon HD voice", voice, mapped)
		}
		if other, ok := seen[mapped]; ok {
			t.Errorf("%s and %s both map to %s", voice, other, mapped)
		}
		seen[mapped] = voice
	}
	if len(voiceMap) != len(voices) {
		t.Errorf("voiceMap has %d entries, want %d", len(voiceMap), len(voices))
	}
}

func TestSynthesizeRejectsUnsupportedFormatsBeforeRequest(t *testing.T) {
	for _, format := range []string{"aac", "flac", "unknown"} {
		t.Run(format, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("unsupported format sent upstream")
				return nil, nil
			})}
			synthesizer, err := NewSynthesizer("https://speech.test", "tts", WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			results := 0
			for result, err := range synthesizer.Synthesize(context.Background(), "Hello", &provider.SynthesizeOptions{Format: format}) {
				results++
				if result != nil || provider.CodeFromError(err, 0) != http.StatusBadRequest {
					t.Fatalf("result = %v, error = %v; want a validation error", result, err)
				}
			}
			if results != 1 {
				t.Fatalf("results = %d", results)
			}
		})
	}
}

func TestTranscribeResourceRequest(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://speech.test/speechtotext/transcriptions:transcribe?api-version=2025-10-15" {
			t.Errorf("request = %s %s", req.Method, req.URL)
		}
		if req.Header.Get("Ocp-Apim-Subscription-Key") != "test-key" {
			t.Error("missing resource key")
		}
		reader, err := req.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		parts := 0
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			parts++
			switch part.FormName() {
			case "audio":
				data, err := io.ReadAll(part)
				if err != nil || string(data) != "audio bytes" || part.FileName() != "hello.wav" || part.Header.Get("Content-Type") != "audio/wav" {
					t.Errorf("unexpected audio part: data=%q, error=%v", data, err)
				}
			case "definition":
				var definition struct {
					Locales     []string `json:"locales"`
					Diarization struct {
						Enabled     bool `json:"enabled"`
						MaxSpeakers int  `json:"maxSpeakers"`
					} `json:"diarization"`
				}
				if err := json.UnmarshalRead(part, &definition); err != nil {
					t.Fatal(err)
				}
				if len(definition.Locales) != 1 || definition.Locales[0] != "de-CH" || !definition.Diarization.Enabled || definition.Diarization.MaxSpeakers != 2 {
					t.Errorf("unexpected definition: %+v", definition)
				}
			default:
				t.Errorf("unexpected multipart field: %s", part.FormName())
			}
		}
		if parts != 2 {
			t.Errorf("multipart fields = %d", parts)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{
			"durationMilliseconds": 1500,
			"combinedPhrases": [{"text":"Grüezi!"}],
			"phrases": [{"text":"Grüezi!", "locale":"de-CH", "speaker":1, "offsetMilliseconds":100, "durationMilliseconds":900,
				"words":[{"text":"Grüezi!", "offsetMilliseconds":100, "durationMilliseconds":900}]}]
		}`))}, nil
	})}
	transcriber, err := NewTranscriber("https://speech.test/", "azure-stt", WithToken("test-key"), WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	for result, err := range transcriber.Transcribe(context.Background(), provider.File{Name: "hello.wav", ContentType: "audio/wav", Content: []byte("audio bytes")}, &provider.TranscribeOptions{Languages: []string{"de-CH"}, Diarize: true, Timestamps: true}) {
		if err != nil {
			t.Fatal(err)
		}
		results++
		if result.Text != "Grüezi!" || result.Language != "de-CH" || result.Duration != 1.5 || result.Model != "azure-stt" || result.ID == "" {
			t.Errorf("unexpected transcription: %+v", result)
		}
		if len(result.Segments) != 1 || result.Segments[0].Speaker != "1" || result.Segments[0].Start != 0.1 || result.Segments[0].End != 1 {
			t.Errorf("unexpected segments: %+v", result.Segments)
		}
		if len(result.Words) != 1 || result.Words[0].Word != "Grüezi!" || result.Words[0].Start != 0.1 || result.Words[0].End != 1 {
			t.Errorf("unexpected words: %+v", result.Words)
		}
	}
	if results != 1 {
		t.Fatalf("results = %d", results)
	}
}

func TestTranscribeLocales(t *testing.T) {
	for _, tt := range []struct {
		languages []string
		locales   string
	}{
		{nil, `{}`},
		{[]string{"de"}, `{"locales":["de-DE"]}`},
		{[]string{"EN"}, `{"locales":["en-US"]}`},
		{[]string{"pt", "no"}, `{"locales":["pt-BR","nb-NO"]}`},
		{[]string{"bn", "tl", "yue"}, `{"locales":["bn-IN","fil-PH","yue-CN"]}`},
		{[]string{"de-CH"}, `{"locales":["de-CH"]}`},
		{[]string{"be"}, `{"locales":["be"]}`},
	} {
		t.Run(strings.Join(tt.languages, ","), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if err := req.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				if got := req.MultipartForm.Value["definition"]; len(got) != 1 || got[0] != tt.locales {
					t.Errorf("definition = %v, want %s", got, tt.locales)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"combinedPhrases":[{"text":"Hallo"}]}`))}, nil
			})}
			transcriber, err := NewTranscriber("https://speech.test", "azure-stt", WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			for _, err := range transcriber.Transcribe(context.Background(), provider.File{Name: "hello.wav", Content: []byte("audio")}, &provider.TranscribeOptions{Languages: tt.languages}) {
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestWhisperLanguagesMapToLocales(t *testing.T) {
	// Whisper's languages, minus Belarusian and Māori, which Azure doesn't transcribe.
	for code := range strings.FieldsSeq("af ar hy az bs bg ca zh hr cs da nl en et fi fr gl de el he hi hu is id it ja kn kk ko lv lt mk ms mr ne no fa pl pt ro ru sr sk sl es sw sv tl ta th tr uk ur vi cy") {
		if _, ok := localeMap[code]; !ok {
			t.Errorf("no locale for %s", code)
		}
	}
}
