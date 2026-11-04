package api

type Result struct {
	Index int `json:"index,omitzero"`

	Score   float64 `json:"score,omitzero"`
	Segment `json:",embed"`
}

type Segment struct {
	Text string `json:"text"`
}

type Document struct {
	Text string `json:"text,omitzero"`

	Pages  []Page  `json:"pages,omitempty"`
	Blocks []Block `json:"blocks,omitempty"`
}

type Page struct {
	Page int `json:"page,omitzero"`

	Unit   string  `json:"unit,omitzero"`
	Width  float64 `json:"width,omitzero"`
	Height float64 `json:"height,omitzero"`
}

type BlockState string

const (
	BlockStateNone      BlockState = ""
	BlockStateChecked   BlockState = "checked"
	BlockStateUnchecked BlockState = "unchecked"
)

type Block struct {
	Page int `json:"page,omitzero"`

	Text  string     `json:"text,omitzero"`
	State BlockState `json:"state,omitzero"`

	Score   float64      `json:"score,omitzero"`
	Polygon [][2]float64 `json:"polygon,omitempty"` // [[x1, y1], [x2, y2], [x3, y3], ...]
}
