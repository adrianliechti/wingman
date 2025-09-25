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
	"github.com/adrianliechti/wingman/pkg/provider"
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

func (c *Client) Extract(ctx context.Context, input extractor.Input, options *extractor.ExtractOptions) (*provider.File, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	if !isSupported(input) {
		return nil, extractor.ErrUnsupported
	}

	if options.Format != nil {
		if *options.Format != extractor.FormatText {
			return nil, extractor.ErrUnsupported
		}
	}

	var data bytes.Buffer
	w := multipart.NewWriter(&data)

	file, err := w.CreateFormFile("files", input.File.Name)

	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(file, bytes.NewReader(input.File.Content)); err != nil {
		return nil, err
	}

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.url, "/")+"/v1/convert/file/async", &data)
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

func (c *Client) readDocument(ctx context.Context, taskID string) (*provider.File, error) {
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

	if task.Document.Text != "" {
		return &provider.File{
			Name: task.Document.Filename,

			Content:     []byte(task.Document.Text),
			ContentType: "text/plain",
		}, nil
	}

	if task.Document.Html != "" {
		return &provider.File{
			Name: task.Document.Filename,

			Content:     []byte(task.Document.Html),
			ContentType: "text/html",
		}, nil
	}

	if task.Document.Markdown != "" {
		return &provider.File{
			Name: task.Document.Filename,

			Content:     []byte(task.Document.Markdown),
			ContentType: "text/markdown",
		}, nil
	}

	if task.Document.Json != "" {
		return &provider.File{
			Name: task.Document.Filename,

			Content:     []byte(task.Document.Json),
			ContentType: "application/json",
		}, nil
	}

	return nil, errors.New("no content")
}

func isSupported(input extractor.Input) bool {
	if input.File == nil {
		return false
	}

	if input.File.Name != "" {
		ext := strings.ToLower(path.Ext(input.File.Name))

		if slices.Contains(SupportedExtensions, ext) {
			return true
		}
	}

	if input.File.ContentType != "" {
		if slices.Contains(SupportedMimeTypes, input.File.ContentType) {
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
