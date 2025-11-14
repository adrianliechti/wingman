package exa

type SearchRequest struct {
	Query string `json:"query"`

	Contents SearchContents `json:"contents,omitempty"`
}

type SearchContents struct {
	Text bool `json:"text,omitempty"`

	LiveCrawl LiveCrawl `json:"livecrawl,omitempty"`
}

type LiveCrawl string

const (
	LiveCrawlAuto     LiveCrawl = "auto"
	LiveCrawlAlways   LiveCrawl = "always"
	LiveCrawlNever    LiveCrawl = "never"
	LiveCrawlFallback LiveCrawl = "fallback"
)

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

type SearchResult struct {
	ID string `json:"id"`

	URL   string `json:"url"`
	Title string `json:"title"`

	Text string `json:"text"`
}

// Research API types

type ResearchModel string

const (
	ResearchModelFast     ResearchModel = "exa-research-fast"
	ResearchModelStandard ResearchModel = "exa-research"
	ResearchModelPro      ResearchModel = "exa-research-pro"
)

type ResearchStatus string

const (
	ResearchStatusPending   ResearchStatus = "pending"
	ResearchStatusRunning   ResearchStatus = "running"
	ResearchStatusCompleted ResearchStatus = "completed"
	ResearchStatusCanceled  ResearchStatus = "canceled"
	ResearchStatusFailed    ResearchStatus = "failed"
)

type CreateTaskRequest struct {
	Instructions string        `json:"instructions"`
	Model        ResearchModel `json:"model,omitempty"`
}

type TaskResponse struct {
	ResearchID string `json:"researchId"`

	CreatedAt    int64  `json:"createdAt"`
	Instructions string `json:"instructions"`

	Status ResearchStatus `json:"status,omitempty"`

	Model  *ResearchModel      `json:"model,omitempty"`
	Output *TaskResponseOutput `json:"output,omitempty"`
}

type TaskResponseOutput struct {
	Content string `json:"content"`
}
