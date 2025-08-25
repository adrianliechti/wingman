package image

// https://platform.openai.com/docs/api-reference/images/create
type ImageCreateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`

	ResponseFormat string `json:"response_format,omitempty"`
}

// https://platform.openai.com/docs/api-reference/images/create
type ImageList struct {
	Images []Image `json:"data"`
}

// https://platform.openai.com/docs/api-reference/images/object
type Image struct {
	URL     string `json:"url,omitempty"`
	B64JSON string `json:"b64_json,omitempty"`

	RevisedPrompt string `json:"revised_prompt,omitempty"`
}
