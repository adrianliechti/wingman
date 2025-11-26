package docling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path"
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
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" {
		return nil, errors.New("invalid url")
	}

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

	f, _ := w.CreateFormFile("files", file.Name)

	if _, err := io.Copy(f, bytes.NewReader(file.Content)); err != nil {
		return nil, err
	}

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.url, "/")+"/v1/convert/file/async", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var convertResult struct {
		TaskID string `json:"task_id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&convertResult); err != nil {
		return nil, err
	}

	if err := c.awaitTask(ctx, convertResult.TaskID); err != nil {
		return nil, err
	}

	return c.readDocument(ctx, convertResult.TaskID)
}

func (c *Client) awaitTask(ctx context.Context, taskID string) error {
	for {
		time.Sleep(4 * time.Second)

		req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.url, "/")+"/v1/status/poll/"+taskID, nil)

		resp, err := c.client.Do(req)

		if err != nil {
			return err
		}

		defer resp.Body.Close()

		var task TaskResult

		if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
			return err
		}

		if task.TaskStatus == TaskStatusStarted {
			continue
		}

		if task.TaskStatus == TaskStatusSuccess {
			return nil
		}

		return errors.New("task failed")
	}
}

func (c *Client) readDocument(ctx context.Context, taskID string) (*extractor.Document, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.url, "/")+"/v1/result/"+taskID, nil)

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	var task TaskResult

	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, err
	}

	if task.TaskStatus != TaskStatusSuccess {
		return nil, errors.New("task not successful")
	}

	var text string

	if task.Document.Html != "" {
		text = task.Document.Html
	}

	if task.Document.Json != "" {
		text = task.Document.Json
	}

	if task.Document.Text != "" {
		text = task.Document.Text
	}

	if task.Document.Markdown != "" {
		text = task.Document.Markdown
	}

	if text == "" {
		return nil, errors.New("no document content")
	}

	return &extractor.Document{
		Text: text,
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
