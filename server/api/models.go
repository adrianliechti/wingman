package api

type Result struct {
	Index int `json:"index,omitempty"`

	Score   float64 `json:"score,omitempty"`
	Segment `json:",inline"`
}

type Segment struct {
	Text string `json:"text"`
}

type Document struct {
	Text string `json:"text,omitempty"`

	Pages  []Page  `json:"pages,omitempty"`
	Blocks []Block `json:"blocks,omitempty"`
}

type Page struct {
	Page int `json:"page"`

	Width  int `json:"width"`
	Height int `json:"height"`
}

type Block struct {
	Page int    `json:"page,omitempty"`
	Text string `json:"text,omitempty"`

	Polygon [][2]int `json:"polygon,omitempty"` // [[x1, y1], [x2, y2], [x3, y3], ...]
}
