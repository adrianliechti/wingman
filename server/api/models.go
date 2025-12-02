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
	Page int `json:"page,omitempty"`

	Unit   string  `json:"unit,omitempty"`
	Width  float64 `json:"width,omitempty"`
	Height float64 `json:"height,omitempty"`
}

type BlockState string

const (
	BlockTypeNone       BlockState = ""
	BlockStateChecked   BlockState = "checked"
	BlockStateUnchecked BlockState = "unchecked"
)

type Block struct {
	Page int `json:"page,omitempty"`

	Text  string     `json:"text,omitempty"`
	State BlockState `json:"state,omitempty"`

	Score   float64      `json:"score,omitempty"`
	Polygon [][2]float64 `json:"polygon,omitempty"` // [[x1, y1], [x2, y2], [x3, y3], ...]
}
