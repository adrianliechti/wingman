package openai

// https://platform.openai.com/docs/api-reference/audio/createSpeech
type SpeechRequest struct {
	Model string `json:"model"`

	Input string `json:"input"`
	Voice string `json:"voice"`
}

type Transcription struct {
	Task string `json:"task"`

	Language string  `json:"language"`
	Duration float64 `json:"duration"`

	Text string `json:"text"`
}
