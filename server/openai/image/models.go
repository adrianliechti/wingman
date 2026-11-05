package image

// https://platform.openai.com/docs/api-reference/images/create
type ImageCreateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`

	Size        string `json:"size,omitzero"`
	AspectRatio string `json:"aspect_ratio,omitzero"`

	Quality    string `json:"quality,omitzero"`
	Resolution string `json:"resolution,omitzero"`
	Complexity string `json:"complexity,omitzero"`

	Background string `json:"background,omitzero"`

	OutputFormat    string `json:"output_format,omitzero"`
	FrameCount      *int64 `json:"frame_count,omitzero"`
	FrameDurationMS *int64 `json:"frame_duration_ms,omitzero"`
	SVG             *struct {
		Model string `json:"model"`
	} `json:"svg,omitzero"`

	ResponseFormat string `json:"response_format,omitzero"`
}

// https://platform.openai.com/docs/api-reference/images/create
type ImageList struct {
	Images []Image `json:"data"`
}

// https://platform.openai.com/docs/api-reference/images/object
type Image struct {
	URL     string `json:"url,omitzero"`
	B64JSON string `json:"b64_json,omitzero"`

	RevisedPrompt string `json:"revised_prompt,omitzero"`
}
