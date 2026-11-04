package audio

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
)

// https://platform.openai.com/docs/api-reference/audio/createSpeech
type SpeechRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`

	Voice SpeechVoice `json:"voice,omitzero"`
	Speed *float32    `json:"speed,omitzero"`

	Instructions string `json:"instructions,omitzero"`

	ResponseFormat string `json:"response_format,omitzero"`

	StreamFormat string `json:"stream_format,omitzero"`
}

// SpeechVoice accepts either a built-in voice name ("alloy") or a custom voice
// reference object ({"id": "voice_1234"}).
type SpeechVoice string

func (v *SpeechVoice) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case '"', 'n':
		var name string
		if err := json.UnmarshalDecode(dec, &name); err != nil {
			return err
		}
		*v = SpeechVoice(name)
	case '{':
		var custom struct {
			ID string `json:"id"`
		}
		if err := json.UnmarshalDecode(dec, &custom); err != nil {
			return err
		}
		if custom.ID == "" {
			return errors.New("voice must be a string or an object with id")
		}
		*v = SpeechVoice(custom.ID)
	default:
		return errors.New("voice must be a string or an object with id")
	}
	return nil
}

type SpeechDeltaEvent struct {
	Type string `json:"type"`

	Audio string `json:"audio"`
}

type SpeechDoneEvent struct {
	Type string `json:"type"`
}

type TranscriptionLanguage struct {
	Code string `json:"code"`
}

type TranscriptionWord struct {
	Word string `json:"word"`

	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// https://platform.openai.com/docs/api-reference/audio/json-object
type Transcription struct {
	Task string `json:"task,omitzero"`

	Language string  `json:"language,omitzero"`
	Duration float64 `json:"duration,omitzero"`

	Text string `json:"text"`

	Languages []TranscriptionLanguage `json:"languages,omitempty"`
}

// https://platform.openai.com/docs/api-reference/audio/verbose-json-object
type VerboseTranscription struct {
	Task string `json:"task"`

	Language string  `json:"language,omitzero"`
	Duration float64 `json:"duration"`

	Text string `json:"text"`

	Segments []VerboseTranscriptionSegment `json:"segments,omitempty"`
	Words    []TranscriptionWord           `json:"words,omitempty"`
}

type VerboseTranscriptionSegment struct {
	ID int `json:"id"`

	Start float64 `json:"start"`
	End   float64 `json:"end"`

	Text string `json:"text"`
}

// https://platform.openai.com/docs/api-reference/audio/diarized-json-object
type DiarizedTranscription struct {
	Task string `json:"task"`

	Duration float64 `json:"duration"`

	Text string `json:"text"`

	Segments []DiarizedTranscriptionSegment `json:"segments,omitempty"`
}

type DiarizedTranscriptionSegment struct {
	Type string `json:"type"`

	ID string `json:"id"`

	Speaker string `json:"speaker"`

	Start float64 `json:"start"`
	End   float64 `json:"end"`

	Text string `json:"text"`
}

type TranscriptionDeltaEvent struct {
	Type string `json:"type"`

	Delta string `json:"delta"`
}

type TranscriptionSegmentEvent struct {
	Type string `json:"type"`

	ID string `json:"id"`

	Speaker string `json:"speaker"`

	Start float64 `json:"start"`
	End   float64 `json:"end"`

	Text string `json:"text"`
}

type TranscriptionDoneEvent struct {
	Type string `json:"type"`

	Text string `json:"text"`

	Languages []TranscriptionLanguage `json:"languages,omitempty"`
}
