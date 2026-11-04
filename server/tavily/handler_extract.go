package tavily

import (
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/server/web"

	"github.com/google/uuid"
)

func (h *Handler) handleExtract(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	var request ExtractRequest
	if err := web.ReadCompatibleRequest(w, r, &request); err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	model, err := request.Provider(r)
	if err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	if err := web.ValidateURLs(request.URLs); err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	timeout := 10 * time.Second
	if request.Timeout != nil {
		if *request.Timeout < 1 || *request.Timeout > 60 {
			web.WriteError(w, r, 400, "timeout must be between 1 and 60 seconds")
			return
		}
		timeout = time.Duration(*request.Timeout * float64(time.Second))
	}
	p, err := h.Scraper(r, model)
	if err != nil {
		web.WriteError(w, r, web.StatusOf(err), err.Error())
		return
	}
	response := ExtractResponse{Results: []ExtractResult{}, FailedResults: []FailedResult{}, RequestID: uuid.NewString()}
	for _, result := range web.Extract(r.Context(), p, request.URLs, timeout) {
		if result.Err != nil {
			response.FailedResults = append(response.FailedResults, FailedResult{URL: result.URL, Error: result.Err.Error()})
		} else {
			response.Results = append(response.Results, ExtractResult{URL: result.URL, RawContent: result.Text, Images: []string{}})
		}
	}
	response.ResponseTime = time.Since(started).Seconds()
	web.WriteJSON(w, response)
}
