package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func valueURL(r *http.Request) string {
	if val := r.FormValue("url"); val != "" {
		return val
	}

	return ""
}

func valueModel(r *http.Request) string {
	if val := r.FormValue("model"); val != "" {
		return val
	}

	return ""
}

func valueQuery(r *http.Request) string {
	if val := r.FormValue("query"); val != "" {
		return val
	}

	return ""
}

func valueInput(r *http.Request) string {
	if val := r.FormValue("input"); val != "" {
		return val
	}

	if val := r.FormValue("prompt"); val != "" {
		return val
	}

	if val := r.FormValue("text"); val != "" {
		return val
	}

	return ""
}

func valueFormat(r *http.Request) string {
	if val := r.FormValue("format"); val != "" {
		return val
	}

	return ""
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

func valueSchema(r *http.Request) (*provider.Schema, error) {
	val := r.FormValue("schema")

	if val == "" {
		return nil, nil
	}

	var schema map[string]any

	if err := json.Unmarshal([]byte(val), &schema); err != nil {
		return nil, err
	}

	return &provider.Schema{
		Name:        "output_schema",
		Description: "the schema for output data in json",

		Schema: schema,
	}, nil
}

func (h *Handler) readText(r *http.Request) (string, error) {
	if val := r.FormValue("text"); val != "" {
		return val, nil
	}

	file, err := h.readContent(r)

	if err != nil {
		return "", err
	}

	return string(file.Content), nil
}

func (h *Handler) readContent(r *http.Request) (*provider.File, error) {
	input := extractor.Input{}

	if url := valueURL(r); url != "" {
		input.URL = url
	} else {
		file, err := readFile(r)

		if err != nil {
			return nil, err
		}

		input.File = file
	}

	e, err := h.Extractor("")

	if err != nil {
		return nil, err
	}

	document, err := e.Extract(r.Context(), input, nil)

	if err != nil {
		return nil, err
	}

	return &provider.File{
		Name: "file.txt",

		Content:     []byte(document.Content),
		ContentType: document.ContentType,
	}, nil
}

func readFile(r *http.Request) (*provider.File, error) {
	if err := r.ParseMultipartForm(32 << 20); err == nil {
		if file, header, err := r.FormFile("file"); err == nil {
			data, err := io.ReadAll(file)

			if err != nil {
				return nil, err
			}

			return &provider.File{
				Name: header.Filename,

				Content:     data,
				ContentType: header.Header.Get("Content-Type"),
			}, nil
		}

		return nil, errors.New("no file found in multipart form")
	}

	// Handle direct file upload or other content types
	contentType := r.Header.Get("Content-Type")
	contentDisposition := r.Header.Get("Content-Disposition")

	_, params, _ := mime.ParseMediaType(contentDisposition)

	filename := params["filename*"]
	filename = strings.TrimPrefix(filename, "UTF-8''")
	filename = strings.TrimPrefix(filename, "utf-8''")

	if filename == "" {
		filename = params["filename"]
	}

	data, err := io.ReadAll(r.Body)

	if err != nil {
		return nil, err
	}

	return &provider.File{
		Name: filename,

		Content:     data,
		ContentType: contentType,
	}, nil
}

func readFiles(r *http.Request) ([]provider.File, error) {
	var files []provider.File

	// Try to parse multipart form with a reasonable max memory (32MB)
	if err := r.ParseMultipartForm(32 << 20); err == nil {
		if r.MultipartForm == nil || r.MultipartForm.File == nil {
			return nil, errors.New("no files found in multipart form")
		}

		for _, fileHeaders := range r.MultipartForm.File {
			for _, header := range fileHeaders {
				file, err := header.Open()

				if err != nil {
					return nil, err
				}

				defer file.Close()

				data, err := io.ReadAll(file)

				if err != nil {
					return nil, err
				}

				files = append(files, provider.File{
					Name: header.Filename,

					Content:     data,
					ContentType: header.Header.Get("Content-Type"),
				})
			}
		}
	}

	if len(files) == 0 {
		file, err := readFile(r)

		if err != nil {
			return nil, err
		}

		files = append(files, *file)
	}

	return files, nil
}
