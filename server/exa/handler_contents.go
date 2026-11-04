package exa

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"

	"github.com/adrianliechti/wingman/server/web"

	"github.com/google/uuid"
)

// contentSelector reads only the supported output limit. Other vendor rendering
// and excerpt options are deliberately ignored for forward compatibility.
func contentSelector(value jsontext.Value) (bool, int, error) {
	switch string(value) {
	case "", "false", "null":
		return false, 0, nil
	case "true":
		return true, 0, nil
	}
	var options struct {
		MaxCharacters *int `json:"maxCharacters"`
	}
	if err := json.Unmarshal(value, &options); err != nil {
		return false, 0, fmt.Errorf("content selector must be a boolean or an options object with an integer maxCharacters: %w", err)
	}
	if options.MaxCharacters == nil {
		return true, 0, nil
	}
	if *options.MaxCharacters < 1 || *options.MaxCharacters > 1000000 {
		return false, 0, errors.New("maxCharacters must be between 1 and 1000000")
	}
	return true, *options.MaxCharacters, nil
}

func (h *Handler) handleContents(w http.ResponseWriter, r *http.Request) {
	var request ContentsRequest
	if err := web.ReadCompatibleRequest(w, r, &request); err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	model, err := request.Provider(r)
	if err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	if len(request.URLs) != 0 && len(request.IDs) != 0 {
		web.WriteError(w, r, 400, "use urls or ids, not both")
		return
	}
	if len(request.URLs) == 0 {
		request.URLs = request.IDs
	}
	if err := web.ValidateURLs(request.URLs); err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	// This extraction subset defaults to text; search content options remain
	// opt-in, including when an empty contents object is supplied.
	if len(request.Text) == 0 {
		request.Text = jsontext.Value("true")
	}
	text, limit, err := contentSelector(request.Text)
	if err != nil {
		web.WriteError(w, r, 400, err.Error())
		return
	}
	if !text {
		web.WriteError(w, r, 400, "contents requires text retrieval")
		return
	}
	if limit == 0 {
		limit = 10000
	}
	p, err := h.Scraper(r, model)
	if err != nil {
		web.WriteError(w, r, web.StatusOf(err), err.Error())
		return
	}
	response := Response{RequestID: uuid.NewString(), Results: []Result{}, Statuses: []Status{}}
	for _, result := range web.Extract(r.Context(), p, request.URLs, 0) {
		response.Statuses = append(response.Statuses, status(result))
		if result.Err == nil {
			text := web.Truncate(result.Text, limit)
			response.Results = append(response.Results, Result{ID: result.URL, URL: result.URL, Text: &text})
		}
	}
	web.WriteJSON(w, response)
}

func status(e web.Extraction) Status {
	status := Status{ID: e.URL, Status: "success"}
	if e.Err != nil {
		status.Status = "error"
		status.Error = &struct {
			Tag string `json:"tag"`
		}{Tag: "FETCH_ERROR"}
	}
	return status
}
