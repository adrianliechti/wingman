package embeddings

import "encoding/json"

// https://platform.openai.com/docs/api-reference/embeddings/create
type EmbeddingsRequest struct {
	Model string `json:"model"`

	Input any `json:"input"`

	EncodingFormat string `json:"encoding_format,omitempty"`
	Dimensions     *int   `json:"dimensions,omitempty"`
}

func (r *EmbeddingsRequest) UnmarshalJSON(data []byte) error {
	type1 := struct {
		Model string `json:"model"`
		Input string `json:"input"`

		Dimensions     *int   `json:"dimensions,omitempty"`
		EncodingFormat string `json:"encoding_format,omitempty"`
	}{}

	if err := json.Unmarshal(data, &type1); err == nil {
		*r = EmbeddingsRequest{
			Model: type1.Model,
			Input: type1.Input,

			Dimensions:     type1.Dimensions,
			EncodingFormat: type1.EncodingFormat,
		}

		return nil
	}

	type2 := struct {
		Model string   `json:"model"`
		Input []string `json:"input"`

		Dimensions     *int   `json:"dimensions,omitempty"`
		EncodingFormat string `json:"encoding_format,omitempty"`
	}{}

	if err := json.Unmarshal(data, &type2); err == nil {
		*r = EmbeddingsRequest{
			Model: type2.Model,
			Input: type2.Input,

			Dimensions:     type2.Dimensions,
			EncodingFormat: type2.EncodingFormat,
		}

		return nil
	}

	return nil
}

// https://platform.openai.com/docs/api-reference/embeddings/object
type Embedding struct {
	Object string `json:"object"` // "embedding"

	Index     int `json:"index"`
	Embedding any `json:"embedding"` // []float32 or base64 string
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
