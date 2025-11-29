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
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Unit   string  `json:"unit"`
	Words  []Word  `json:"words"`
	//Lines      []Line  `json:"lines"`
}

type Word struct {
	Content string    `json:"content"`
	Polygon []float64 `json:"polygon"`
	//Confidence float64   `json:"confidence"`
	//Span       Span      `json:"span"`
}

// type Line struct {
// 	Content string    `json:"content"`
// 	Polygon []float64 `json:"polygon"`
// 	Spans   []Span    `json:"spans"`
// }

// type Span struct {
// 	Offset int `json:"offset"`
// 	Length int `json:"length"`
// }
