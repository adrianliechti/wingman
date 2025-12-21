package mistral

type Response struct {
	Model string `json:"model"`
	Pages []Page `json:"pages"`
}

type Page struct {
	Index      int         `json:"index"`
	Dimensions *Dimensions `json:"dimensions"`

	Markdown string `json:"markdown"`
}

type Dimensions struct {
	DPI int `json:"dpi"`

	Width  int `json:"width"`
	Height int `json:"height"`
}
