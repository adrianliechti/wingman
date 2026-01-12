package kreuzberg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/extractor"

	"github.com/google/uuid"
)

var _ extractor.Provider = &Client{}

type Client struct {
	client *http.Client

	url   string
	token string
}

func New(url string, options ...Option) (*Client, error) {
	c := &Client{
		client: http.DefaultClient,

		url: url,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Extract(ctx context.Context, file extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	if !isSupported(file) {
		return nil, extractor.ErrUnsupported
	}

	var body bytes.Buffer

	w := multipart.NewWriter(&body)

	if file.ContentType == "" {
		file.ContentType = "application/octet-stream"
	}

	if file.Name == "" {
		if ext, _ := mime.ExtensionsByType(file.ContentType); len(ext) > 0 {
			file.Name = uuid.New().String() + ext[0]
		}
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", multipart.FileContentDisposition("files", file.Name))
	h.Set("Content-Type", file.ContentType)

	f, err := w.CreatePart(h)

	if err != nil {
		return nil, err
	}

	if _, err := f.Write(file.Content); err != nil {
		return nil, err
	}

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.url, "/")+"/extract", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var result []ExtractionResult

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &extractor.Document{
		Text: strings.TrimSpace(result[0].Content),
	}, nil
}

func isSupported(file extractor.File) bool {
	if file.Name != "" {
		ext := strings.ToLower(path.Ext(file.Name))

		if slices.Contains(SupportedExtensions, ext) {
			return true
		}
	}

	if file.ContentType != "" {
		if slices.Contains(SupportedMimeTypes, file.ContentType) {
			return true
		}
	}

	return false
}

func convertError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	if len(data) == 0 {
		return errors.New(http.StatusText(resp.StatusCode))
	}

	return errors.New(string(data))
}
