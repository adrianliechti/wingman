package openai

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestValidateOpenAIRealtimeAudioFormats(t *testing.T) {
	defaults := (&Realtime{}).Defaults()
	for _, encoding := range []provider.RealtimeAudioEncoding{
		provider.RealtimeAudioPCMU,
		provider.RealtimeAudioPCMA,
	} {
		t.Run(string(encoding), func(t *testing.T) {
			options := defaults
			format := provider.RealtimeAudioFormat{
				Encoding: encoding, SampleRate: 8000, SampleSize: 8, Channels: 1,
			}
			options.InputAudio = format
			options.OutputAudio = format
			if err := validateOpenAIRealtimeOptions(options); err != nil {
				t.Fatalf("valid G.711 format rejected: %v", err)
			}

			options.InputAudio.SampleSize = 16
			if err := validateOpenAIRealtimeOptions(options); err == nil {
				t.Fatal("16-bit G.711 format was accepted")
			}
		})
	}
}

func TestOpenAIRealtimeAudioFormatObject(t *testing.T) {
	pcm := openAIAudioFormat(provider.RealtimeAudioFormat{
		Encoding: provider.RealtimeAudioPCM, SampleRate: 24000, SampleSize: 16, Channels: 1,
	})
	if pcm["type"] != "audio/pcm" || pcm["rate"] != 24000 {
		t.Fatalf("PCM format = %#v", pcm)
	}

	g711 := openAIAudioFormat(provider.RealtimeAudioFormat{
		Encoding: provider.RealtimeAudioPCMU, SampleRate: 8000, SampleSize: 8, Channels: 1,
	})
	if g711["type"] != "audio/pcmu" {
		t.Fatalf("PCMU format = %#v", g711)
	}
	if _, ok := g711["rate"]; ok {
		t.Fatalf("PCMU format includes unsupported rate: %#v", g711)
	}
}

func TestOpenAIRealtimeSessionObjectForcesTracingOff(t *testing.T) {
	object := openAISessionObject((&Realtime{}).Defaults())
	tracing, ok := object["tracing"]
	if !ok {
		t.Fatal("session object does not set tracing")
	}
	if tracing != nil {
		t.Fatalf("session tracing = %#v, want null", tracing)
	}
}

func TestTranslateCurrentInputEvents(t *testing.T) {
	session := &openAIRealtimeSession{
		contents:    make(map[string]provider.RealtimeContentType),
		transcripts: make(map[string]bool),
	}

	timeout := session.translate([]byte(`{
		"type":"input_audio_buffer.timeout_triggered",
		"item_id":"item_timeout","audio_start_ms":13216,"audio_end_ms":19232
	}`))
	if len(timeout) != 1 || timeout[0].Type != provider.RealtimeEventInputTimeoutTriggered {
		t.Fatalf("timeout events = %#v", timeout)
	}
	if timeout[0].ItemID != "item_timeout" || timeout[0].AudioStart.Milliseconds() != 13216 || timeout[0].AudioEnd.Milliseconds() != 19232 {
		t.Fatalf("timeout event = %#v", timeout[0])
	}

	segment := session.translate([]byte(`{
		"type":"conversation.item.input_audio_transcription.segment",
		"item_id":"msg_011","content_index":2,"text":"hello","id":"seg_0001",
		"speaker":"spk_1","start":0.1,"end":0.4
	}`))
	if len(segment) != 1 || segment[0].Type != provider.RealtimeEventInputTranscriptionSegment {
		t.Fatalf("segment events = %#v", segment)
	}
	got := segment[0]
	if got.ItemID != "msg_011" || got.ContentIndex != 2 || got.Text != "hello" || got.SegmentID != "seg_0001" || got.Speaker != "spk_1" || got.SegmentStart != 0.1 || got.SegmentEnd != 0.4 {
		t.Fatalf("segment event = %#v", got)
	}
}
