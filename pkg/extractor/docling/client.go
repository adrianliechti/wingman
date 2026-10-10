package docling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

var _ extractor.Provider = &Client{}

type Client struct {
	client *http.Client

	url   string
	token string

	pollInterval time.Duration
}

func New(endpoint string, options ...Option) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, errors.New("docling: invalid http(s) URL")
	}

	c := &Client{
		client: http.DefaultClient,

		url:          strings.TrimRight(endpoint, "/"),
		pollInterval: 4 * time.Second,
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

	if !c.Capabilities().MaySupport(file) {
		return nil, extractor.ErrUnsupported
	}

	var body bytes.Buffer

	w := multipart.NewWriter(&body)

	contentType, _, _ := mime.ParseMediaType(strings.TrimSpace(file.ContentType))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	name := file.Name
	if name == "" {
		name = "document"
		if extensions, _ := mime.ExtensionsByType(contentType); len(extensions) > 0 {
			name += extensions[0]
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", multipart.FileContentDisposition("files", name))
	header.Set("Content-Type", contentType)
	f, err := w.CreatePart(header)
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(f, bytes.NewReader(file.Content)); err != nil {
		return nil, err
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	var convertResult struct {
		TaskID string `json:"task_id"`
	}

	if err := c.request(ctx, http.MethodPost, "/v1/convert/file/async", &body, w.FormDataContentType(), &convertResult); err != nil {
		return nil, err
	}
	if convertResult.TaskID == "" {
		return nil, errors.New("docling: missing task ID")
	}

	if err := c.awaitTask(ctx, convertResult.TaskID); err != nil {
		return nil, err
	}

	return c.readDocument(ctx, convertResult.TaskID)
}

func (c *Client) awaitTask(ctx context.Context, taskID string) error {
	for {
		var task TaskResult
		if err := c.request(ctx, http.MethodGet, "/v1/status/poll/"+url.PathEscape(taskID), nil, "", &task); err != nil {
			return err
		}

		switch task.TaskStatus {
		case TaskStatusSuccess:
			return nil
		case TaskStatusPending, TaskStatusStarted:
			timer := time.NewTimer(c.pollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		default:
			return fmt.Errorf("docling: task status %q", task.TaskStatus)
		}
	}
}

func (c *Client) readDocument(ctx context.Context, taskID string) (*extractor.Document, error) {
	var task TaskResult
	if err := c.request(ctx, http.MethodGet, "/v1/result/"+url.PathEscape(taskID), nil, "", &task); err != nil {
		return nil, err
	}

	if task.Status != "success" && task.Status != "partial_success" && !(task.Status == "" && task.TaskStatus == TaskStatusSuccess) {
		return nil, errors.New("task not successful")
	}
	if task.Document == nil {
		return nil, errors.New("docling: missing document")
	}

	text := task.Document.Markdown
	if text == "" {
		text = task.Document.Text
	}
	if text == "" {
		text = task.Document.Html
	}

	if text == "" && len(task.Document.Json) > 0 && string(task.Document.Json) != "null" {
		text = string(task.Document.Json)
		// Older servers may return serialized JSON as a string.
		if task.Document.Json[0] == '"' {
			if err := json.Unmarshal(task.Document.Json, &text); err != nil {
				return nil, err
			}
		}
	}

	if text == "" {
		return nil, errors.New("no document content")
	}

	return &extractor.Document{
		Text: text,
	}, nil
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader, contentType string, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.token != "" {
		req.Header.Set("X-Api-Key", c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return convertError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

func (c *Client) Capabilities() extractor.Capabilities {
	return extractor.Capabilities{
		MediaTypes: slices.Clone(SupportedMimeTypes),
		Extensions: slices.Clone(SupportedExtensions),
	}
}

func convertError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	if len(data) == 0 {
		return errors.New(http.StatusText(resp.StatusCode))
	}

	return errors.New(string(data))
}
