package files

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

// ContentType normalizes an explicit MIME type and sniffs the bytes when the
// caller supplies no type or the generic application/octet-stream type.
func ContentType(value string, data []byte) string {
	if mediatype, _, err := mime.ParseMediaType(value); err == nil {
		value = mediatype
	}
	if value == "" || value == "application/octet-stream" {
		return http.DetectContentType(data)
	}
	return value
}

// FromMultipart reads an uploaded file and resolves its content type.
func FromMultipart(header *multipart.FileHeader) (*provider.File, error) {
	file, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return &provider.File{
		Name:        header.Filename,
		Content:     data,
		ContentType: ContentType(header.Header.Get("Content-Type"), data),
	}, nil
}

// FromURL loads a file from an HTTP(S) or base64 data URL for any API format.
func FromURL(url string) (*provider.File, error) {
	if url == "" {
		return nil, fmt.Errorf("invalid url")
	}

	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		resp, err := http.Get(url)

		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unable to fetch url (%s): %s", url, resp.Status)
		}

		data, err := io.ReadAll(resp.Body)

		if err != nil {
			return nil, err
		}

		contentType := resp.Header.Get("Content-Type")

		if mediatype, _, err := mime.ParseMediaType(contentType); err == nil {
			contentType = mediatype
		}

		file := provider.File{
			Content:     data,
			ContentType: contentType,
		}

		if ext, _ := mime.ExtensionsByType(file.ContentType); len(ext) > 0 {
			file.Name = uuid.New().String() + ext[0]
		}

		return &file, nil
	}

	if strings.HasPrefix(url, "data:") {
		re := regexp.MustCompile(`data:([a-zA-Z]+\/[a-zA-Z0-9.+_-]+);base64,\s*(.+)`)

		match := re.FindStringSubmatch(url)

		if len(match) != 3 {
			return nil, fmt.Errorf("invalid data url")
		}

		data, err := base64.StdEncoding.DecodeString(match[2])

		if err != nil {
			return nil, fmt.Errorf("invalid data encoding")
		}

		file := provider.File{
			Content:     data,
			ContentType: match[1],
		}

		if ext, _ := mime.ExtensionsByType(file.ContentType); len(ext) > 0 {
			file.Name = uuid.New().String() + ext[0]
		}

		return &file, nil
	}

	return nil, fmt.Errorf("invalid url")
}
