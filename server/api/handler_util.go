package api

import (
	"errors"
	"io"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/files"
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

	if val := r.FormValue("query"); val != "" {
		return val
	}

	if val := r.FormValue("instructions"); val != "" {
		return val
	}

	return ""
}

func valueAspect(r *http.Request) provider.AspectRatio {
	if aspect := provider.ParseAspect(r.FormValue("aspect_ratio")); aspect != "" {
		return aspect
	}

	return provider.ParseSizeAspect(r.FormValue("size"))
}

func acceptFormat(r *http.Request) provider.ImageFormat {
	for value := range strings.SplitSeq(r.Header.Get("Accept"), ",") {
		media := strings.TrimSpace(value)

		if i := strings.IndexByte(media, ';'); i >= 0 {
			media = strings.TrimSpace(media[:i])
		}

		switch strings.ToLower(media) {
		case "image/png":
			return provider.ImageFormatPNG
		case "image/jpeg", "image/jpg":
			return provider.ImageFormatJPEG
		case "image/webp":
			return provider.ImageFormatWEBP
		}
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

func valueCategory(r *http.Request) string {
	if val := r.FormValue("category"); val != "" {
		return val
	}

	return ""
}

func valueLocation(r *http.Request) string {
	if val := r.FormValue("location"); val != "" {
		return val
	}

	return ""
}

func valueLimit(r *http.Request) *int {
	if val := r.FormValue("limit"); val != "" {
		if limit, err := strconv.Atoi(val); err == nil {
			return &limit
		}
	}

	return nil
}

func valueSchema(r *http.Request) (*provider.Schema, error) {
	val := r.FormValue("schema")

	if val == "" {
		return nil, nil
	}

	var properties map[string]any

	if err := jsonutil.Unmarshal([]byte(val), &properties); err != nil {
		return nil, err
	}

	return &provider.Schema{
		Name:        "output_schema",
		Description: "the schema for output data in json",

		Properties: properties,
	}, nil
}

func (h *Handler) readText(r *http.Request) (string, error) {
	if val := valueInput(r); val != "" {
		return val, nil
	}

	if url := valueURL(r); url != "" {
		if err := h.Policy.Verify(r.Context(), policy.ResourceModel, "", policy.ActionAccess); err != nil {
			return "", err
		}

		p, err := h.Scraper("")

		if err != nil {
			return "", err
		}

		result, err := p.Scrape(r.Context(), url, nil)

		if err != nil {
			return "", err
		}

		return result.Text, nil
	}

	file, err := readFile(r)

	if err != nil {
		return "", err
	}

	return h.extractText(r, file)
}

func (h *Handler) extractText(r *http.Request, file *provider.File) (string, error) {
	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, "", policy.ActionAccess); err != nil {
		return "", err
	}

	p, err := h.Extractor("")

	if err != nil {
		return "", err
	}

	result, err := p.Extract(r.Context(), *file, nil)

	if err != nil {
		return "", err
	}

	if result == nil {
		return "", errors.New("extractor returned an empty response")
	}
	return result.Text, nil
}

func readFile(r *http.Request) (*provider.File, error) {
	if err := r.ParseMultipartForm(32 << 20); err == nil {
		if headers := r.MultipartForm.File["file"]; len(headers) > 0 {
			return files.FromMultipart(headers[0])
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

	if len(data) == 0 && filename == "" {
		return nil, errors.New("no file provided")
	}

	return &provider.File{
		Name: filename,

		Content:     data,
		ContentType: files.ContentType(contentType, data),
	}, nil
}

// readNamedFile returns an optional file from an already parsed form.
func readNamedFile(r *http.Request, field string) (*provider.File, error) {
	if r.MultipartForm == nil || len(r.MultipartForm.File[field]) == 0 {
		return nil, nil
	}

	return files.FromMultipart(r.MultipartForm.File[field][0])
}

// readFiles returns every uploaded file except those under the skipped
// multipart fields, falling back to a raw request body.
func readFiles(r *http.Request, skip ...string) ([]provider.File, error) {
	var uploads []provider.File

	if r.MultipartForm != nil {
		// Repeated file parts retain their order. Sort other field names so
		// a guide's target cannot change with Go's map iteration order.
		fields := slices.Sorted(maps.Keys(r.MultipartForm.File))
		if i := slices.Index(fields, "file"); i >= 0 {
			fields = append([]string{"file"}, slices.Delete(fields, i, i+1)...)
		}
		for _, field := range fields {
			if slices.Contains(skip, field) {
				continue
			}

			for _, header := range r.MultipartForm.File[field] {
				file, err := files.FromMultipart(header)
				if err != nil {
					return nil, err
				}
				uploads = append(uploads, *file)
			}
		}
		return uploads, nil
	}

	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.ContentLength == 0 || contentType == "application/x-www-form-urlencoded" {
		return nil, nil
	}

	file, err := readFile(r)
	if err != nil {
		return nil, err
	}
	return []provider.File{*file}, nil
}
