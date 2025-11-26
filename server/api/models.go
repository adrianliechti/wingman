package api

type Result struct {
	Index    int     `json:"index,omitempty"`
	Score    float64 `json:"score,omitempty"`
	Document `json:",inline"`
}

type Segment struct {
	Text string `json:"text"`
}

type Document struct {
	Text string `json:"text,omitempty"`

	Blocks []Block `json:"blocks,omitempty"`
}

type Block struct {
	Page int `json:"page,omitempty"`

	Text string `json:"text,omitempty"`

	Box [4]int `json:"box,omitempty"` // [x1, y1, x2, y2]

	Confidence float64 `json:"confidence,omitempty"`
}
