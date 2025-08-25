package audio

// https://platform.openai.com/docs/api-reference/audio/createSpeech
type SpeechRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`

	Voice string   `json:"voice,omitempty"`
	Speed *float32 `json:"speed,omitempty"`

	Instructions string `json:"instructions,omitempty"`

	ResponseFormat string `json:"response_format,omitempty"`
}

type Transcription struct {
	Task string `json:"task"`

	Language string  `json:"language"`
	Duration float64 `json:"duration"`

	Text string `json:"text"`
}
