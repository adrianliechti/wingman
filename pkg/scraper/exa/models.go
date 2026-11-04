package exa

type ContentsRequest struct {
	URLs []string `json:"urls"`

	Text bool `json:"text"`

	// MaxAgeHours controls content freshness (replaces the deprecated livecrawl).
	// 0 forces a fresh fetch.
	MaxAgeHours int `json:"maxAgeHours"`
}

type ContentsResponse struct {
	Results  []ContentsResult `json:"results"`
	Statuses []ContentsStatus `json:"statuses,omitempty"`
}

type ContentsResult struct {
	ID string `json:"id"`

	URL   string `json:"url"`
	Title string `json:"title"`

	Text string `json:"text"`
}

type ContentsStatus struct {
	ID     string             `json:"id"`
	Status string             `json:"status"`
	Error  *ContentsStatusErr `json:"error,omitzero"`
}

type ContentsStatusErr struct {
	Tag        string `json:"tag,omitzero"`
	HTTPStatus int    `json:"httpStatusCode,omitzero"`
}
