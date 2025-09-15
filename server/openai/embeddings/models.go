package embeddings

import "encoding/json"

// https://platform.openai.com/docs/api-reference/embeddings/create
type EmbeddingsRequest struct {
	Model string `json:"model"`

	Input any `json:"input"`

	// encoding_format string: float, base64
	// dimensions int
	// user string
}

func (r *EmbeddingsRequest) UnmarshalJSON(data []byte) error {
	type1 := struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}{}

	if err := json.Unmarshal(data, &type1); err == nil {
		*r = EmbeddingsRequest{
			Model: type1.Model,
			Input: type1.Input,
		}

		return nil
	}

	type2 := struct {
		Model string `json:"model"`

		Input []string `json:"input"`
	}{}

	if err := json.Unmarshal(data, &type2); err == nil {
		*r = EmbeddingsRequest{
			Model: type2.Model,
			Input: type2.Input,
		}

		return nil
	}

	return nil
}

// https://platform.openai.com/docs/api-reference/embeddings/object
type Embedding struct {
	Object string `json:"object"` // "embedding"

	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// https://platform.openai.com/docs/api-reference/embeddings/create
type EmbeddingList struct {
	Object string `json:"object"` // "list"

	Model string      `json:"model"`
	Data  []Embedding `json:"data"`

	Usage *Usage `json:"usage,omitempty"`
}

type Usage struct {
	PromptTokens int `json:"prompt_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}
