package tavily

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/searcher/tavily"
	"github.com/adrianliechti/wingman/server/web"

	"github.com/google/uuid"
)

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	var request SearchRequest
	if err := web.ReadCompatibleRequest(w, r, &request); err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	model, err := request.Provider(r)
	if err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	limit := 10
	if request.MaxResults != nil {
		limit = *request.MaxResults
	}
	if limit < 0 || limit > 20 {
		web.WriteError(w, r, 400, "max_results must be between 0 and 20")
		return
	}
	topic := request.Topic
	if topic == "" {
		topic = "general"
	}
	if !slices.Contains([]string{"general", "news", "finance"}, topic) {
		web.WriteError(w, r, 400, "topic must be general, news or finance")
		return
	}
	options := searcher.SearchOptions{Limit: &limit, Category: topic, Include: request.Include, Exclude: request.Exclude}
	switch string(request.IncludeRawContent) {
	case "", "null", "false":
	case "true", `"markdown"`, `"text"`:
		options.Text = &searcher.TextOptions{}
	default:
		web.WriteError(w, r, 400, "include_raw_content must be false, true, markdown or text")
		return
	}
	if len(options.Include) > 300 || len(options.Exclude) > 150 {
		web.WriteError(w, r, 400, "at most 300 included and 150 excluded domains are supported")
		return
	}
	if request.Country != "" {
		if topic != "general" {
			web.WriteError(w, r, 400, "country is supported only with the general topic")
			return
		}
		code, ok := tavily.CountryCode(request.Country)
		if !ok {
			web.WriteError(w, r, 400, "unsupported country")
			return
		}
		options.Location = code
	}
	options.Since, options.Until, err = tavilyDates(request, started)
	if err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	results, err := h.Search(r, model, request.Query, &options)
	if err != nil {
		web.WriteError(w, r, web.StatusOf(err), err.Error())
		return
	}
	response := SearchResponse{Query: request.Query, Images: []string{}, Results: []SearchResult{}, RequestID: uuid.NewString()}
	for _, result := range results {
		hit := SearchResult{URL: result.Source, Title: result.Title, Content: result.Excerpt, Score: result.Score}
		if options.Text != nil && result.Text != nil && *result.Text != "" {
			hit.RawContent = result.Text
		}
		if result.Timestamp != nil && (request.IncludePublishedDate || topic == "news") {
			hit.PublishedDate = result.Timestamp.UTC().Format(http.TimeFormat)
		}
		response.Results = append(response.Results, hit)
	}
	response.ResponseTime = time.Since(started).Seconds()
	web.WriteJSON(w, response)
}

func tavilyDates(request SearchRequest, now time.Time) (*time.Time, *time.Time, error) {
	var since, until *time.Time
	if request.Start != "" {
		t, err := time.Parse("2006-01-02", request.Start)
		if err != nil {
			return nil, nil, errors.New("start_date must be YYYY-MM-DD")
		}
		since = &t
	}
	if request.End != "" {
		t, err := time.Parse("2006-01-02", request.End)
		if err != nil {
			return nil, nil, errors.New("end_date must be YYYY-MM-DD")
		}
		t = t.AddDate(0, 0, 1) // Tavily's calendar end date is inclusive.
		until = &t
	}
	if request.TimeRange != "" && request.Days != nil {
		return nil, nil, errors.New("use time_range or days, not both")
	}
	days := 0
	if request.TimeRange != "" {
		switch request.TimeRange {
		case "d", "day":
			days = 1
		case "w", "week":
			days = 7
		case "m", "month":
			days = 30
		case "y", "year":
			days = 365
		default:
			return nil, nil, errors.New("time_range must be day, week, month or year")
		}
	}
	// Like upstream, days applies only to news; SDK helpers send it with general.
	if request.Days != nil && request.Topic == "news" {
		if *request.Days < 1 || *request.Days > 36500 {
			return nil, nil, errors.New("days must be between 1 and 36500")
		}
		days = *request.Days
	}
	if days != 0 {
		t := now.UTC().AddDate(0, 0, -days)
		if since == nil || t.After(*since) {
			since = &t
		}
	}
	return since, until, nil
}
