package azure

type OperationStatus string

const (
	OperationStatusSucceeded  OperationStatus = "succeeded"
	OperationStatusRunning    OperationStatus = "running"
	OperationStatusNotStarted OperationStatus = "notStarted"
)

type AnalyzeOperation struct {
	Status OperationStatus `json:"status"`

	Result AnalyzeResult `json:"analyzeResult"`
}

type AnalyzeResult struct {
	ModelID string `json:"modelId"`

	Content string `json:"content"`
	Pages   []Page `json:"pages"`
}

type Page struct {
	PageNumber int `json:"pageNumber"`
	//Angle      float64 `json:"angle"`

	Unit   string  `json:"unit"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`

	Words          []Word          `json:"words"`
	SelectionMarks []SelectionMark `json:"selectionMarks"`
}

type Word struct {
	Content string `json:"content"`

	Span    Span      `json:"span"`
	Polygon []float64 `json:"polygon"`

	Confidence float64 `json:"confidence"`
}

type SelectionMark struct {
	State string `json:"state"`

	Span    Span      `json:"span"`
	Polygon []float64 `json:"polygon"`

	Confidence float64 `json:"confidence"`
}

type Span struct {
	Offset int `json:"offset"`
	Length int `json:"length"`
}
