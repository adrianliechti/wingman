package api

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func valueModel(r *http.Request) string {
	if val := r.FormValue("model"); val != "" {
		return val
	}

	return ""
}

func valueURL(r *http.Request) string {
	if val := r.FormValue("url"); val != "" {
		return val
	}

	return ""
}

func valueSchema(r *http.Request) (*provider.Schema, error) {
	val := r.FormValue("schema")

	if val == "" {
		return nil, nil
	}

	var schema struct {
		Name        string
		Description string

		Strict *bool

		Schema map[string]any
	}

	if err := json.Unmarshal([]byte(val), &schema); err != nil {
		return nil, err
	}

	return &provider.Schema{
		Name:        schema.Name,
		Description: schema.Description,

		Strict: schema.Strict,

		Schema: schema.Schema,
	}, nil
}

func valueLanguage(r *http.Request) string {
	if val := r.FormValue("lang"); val != "" {
		return val
	}

	if val := r.FormValue("language"); val != "" {
		return val
	}

	return ""
}

func (h *Handler) readText(r *http.Request) (string, error) {
	_, reader, err := h.readContent(r)

	if err != nil {
		return "", err
	}

	defer reader.Close()

	data, err := io.ReadAll(reader)

	if err != nil {
		return "", err
	}

	return string(data), nil
}

func (h *Handler) readContent(r *http.Request) (string, io.ReadCloser, error) {
	e, err := h.Extractor("")

	if err != nil {
		return "", nil, err
	}

	input := extractor.File{
		URL: r.FormValue("url"),
	}

	if input.URL == "" {
		name, reader, err := h.readFile(r)

		if err != nil {
			return "", nil, err
		}

		input.Name = name
		input.Reader = reader
	}

	document, err := e.Extract(r.Context(), input, nil)

	if err != nil {
		return "", nil, err
	}

	return "file.txt", io.NopCloser(strings.NewReader(document.Content)), nil
}

func (h *Handler) readFile(r *http.Request) (string, io.ReadCloser, error) {
	if file, header, err := r.FormFile("file"); err == nil {
		return header.Filename, file, nil
	}

	contentType := r.Header.Get("Content-Type")
	contentDisposition := r.Header.Get("Content-Disposition")

	_, params, _ := mime.ParseMediaType(contentDisposition)

	filename := params["filename*"]
	filename = strings.TrimPrefix(filename, "UTF-8''")
	filename = strings.TrimPrefix(filename, "utf-8''")

	if filename == "" {
		filename = params["filename"]
	}

	_ = contentType
	_ = contentDisposition

	return filename, r.Body, nil
}
