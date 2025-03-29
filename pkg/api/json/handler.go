package json

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/api"
	"github.com/adrianliechti/wingman/pkg/provider"

	"gopkg.in/yaml.v3"
)

var _ api.Provider = (*Handler)(nil)

type Handler struct {
	input  *api.Schema
	output *api.Schema

	completer provider.Completer
}

func New(options ...Option) (*Handler, error) {
	h := &Handler{}

	for _, option := range options {
		option(h)
	}

	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input string

	mediatype, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch mediatype {
	case "application/json":
		var body map[string]any

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		data, _ := json.MarshalIndent(body, "", "  ")
		input = string(data)

	case "application/yaml":
		var body map[string]any

		if err := yaml.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		data, _ := json.MarshalIndent(body, "", "  ")
		input = string(data)

	case "text/plain":
		data, err := io.ReadAll(r.Body)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		input = string(data)

	default:
		http.Error(w, "unsupported content type", http.StatusBadRequest)
		return
	}

	var system strings.Builder

	if h.input != nil {
		system.WriteString("## Input (`" + h.input.Name + "`):\n\n")
		system.WriteString(h.input.Description)
		system.WriteString("\n\n")

		schema, _ := json.MarshalIndent(h.input.Schema, "", "  ")

		system.WriteString("Input Schema:")
		system.WriteString("\n```json\n")
		system.WriteString(string(schema))
		system.WriteString("\n```\n\n")
	}

	if h.output != nil {
		system.WriteString("## Output (`" + h.output.Name + "`):\n\n")
		system.WriteString(h.output.Description)
		system.WriteString("\n\n")

		schema, _ := json.MarshalIndent(h.output.Schema, "", "  ")

		system.WriteString("Output Schema:")
		system.WriteString("\n```json\n")
		system.WriteString(string(schema))
		system.WriteString("\n```\n\n")
	}

	messages := []provider.Message{}

	if system.Len() > 0 {
		messages = append(messages, provider.SystemMessage(system.String()))
	}

	if input != "" {
		messages = append(messages, provider.UserMessage(input))
	}

	options := &provider.CompleteOptions{
		Format: provider.CompletionFormatJSON,
		Schema: h.output,
	}

	completion, err := h.completer.Complete(r.Context(), messages, options)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	content := completion.Message.Content.Text()
	contentType := "application/json"

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(content))
}
