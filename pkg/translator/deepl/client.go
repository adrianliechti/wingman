package deepl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/translator"
)

var (
	_ translator.Provider = (*Client)(nil)
)

type Client struct {
	client *http.Client

	url   string
	token string
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" {
		url = "https://api-free.deepl.com"
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

func (c *Client) Translate(ctx context.Context, input translator.Input, options *translator.TranslateOptions) (*provider.File, error) {
	if options == nil {
		options = new(translator.TranslateOptions)
	}

	if options.Language == "" {
		options.Language = "en"
	}

	if input.File != nil {
		return c.translateFile(ctx, input.File, options.Language)
	}

	return c.translateText(ctx, input.Text, options.Language)
}

func (c *Client) translateText(ctx context.Context, input, language string) (*provider.File, error) {
	type bodyType struct {
		Text       []string `json:"text"`
		TargetLang string   `json:"target_lang"`
	}

	body := bodyType{
		Text: []string{
			strings.TrimSpace(input),
		},

		TargetLang: language,
	}

	u, _ := url.JoinPath(c.url, "/v2/translate")
	r, _ := http.NewRequestWithContext(ctx, "POST", u, jsonReader(body))
	r.Header.Add("Authorization", "DeepL-Auth-Key "+c.token)
	r.Header.Add("Content-Type", "application/json")

	resp, err := c.client.Do(r)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	type resultType struct {
		Translations []struct {
			DetectedSourceLanguage string `json:"detected_source_language"`
			Text                   string `json:"text"`
		} `json:"translations"`
	}

	var result resultType

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Translations) == 0 {
		return nil, errors.New("unable to translate content")
	}

	return &provider.File{
		Content:     []byte(result.Translations[0].Text),
		ContentType: "text/plain",
	}, nil
}

func (c *Client) translateFile(ctx context.Context, input *provider.File, language string) (*provider.File, error) {
	id, key, err := c.uploadDocument(ctx, input, language)

	if err != nil {
		return nil, err
	}

	if err := c.waitDocument(ctx, id, key); err != nil {
		return nil, err
	}

	return c.downloadDocument(ctx, id, key)
}

func (c *Client) uploadDocument(ctx context.Context, input *provider.File, language string) (string, string, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	w.WriteField("target_lang", language)

	f, err := w.CreateFormFile("file", input.Name)

	if err != nil {
		return "", "", err
	}

	if _, err := f.Write(input.Content); err != nil {
		return "", "", err
	}

	w.Close()

	u, _ := url.JoinPath(c.url, "/v2/document")
	r, _ := http.NewRequestWithContext(ctx, "POST", u, &b)
	r.Header.Add("Authorization", "DeepL-Auth-Key "+c.token)
	r.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.client.Do(r)

	if err != nil {
		return "", "", err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", convertError(resp)
	}

	type resultType struct {
		DocumentID  string `json:"document_id"`
		DocumentKey string `json:"document_key"`
	}

	var result resultType

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}

	return result.DocumentID, result.DocumentKey, nil
}

func (c *Client) waitDocument(ctx context.Context, documentID, documentKey string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			ok, err := c.checkDocument(ctx, documentID, documentKey)

			if err != nil {
				return err
			}

			if ok {
				return nil
			}

			time.Sleep(2 * time.Second)
		}
	}
}

func (c *Client) checkDocument(ctx context.Context, documentID, documentKey string) (bool, error) {
	type statusRequest struct {
		DocumentKey string `json:"document_key"`
	}

	body := statusRequest{
		DocumentKey: documentKey,
	}

	u, _ := url.JoinPath(c.url, "/v2/document/"+documentID)
	r, _ := http.NewRequestWithContext(ctx, "POST", u, jsonReader(body))
	r.Header.Add("Authorization", "DeepL-Auth-Key "+c.token)
	r.Header.Add("Content-Type", "application/json")

	resp, err := c.client.Do(r)

	if err != nil {
		return false, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, convertError(resp)
	}

	type resultType struct {
		DocumentID string `json:"document_id"`

		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	}

	var result resultType

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}

	if result.Status == "done" {
		return true, nil
	}

	if result.Status == "error" {
		return false, errors.New(result.Message)
	}

	return false, nil
}

func (c *Client) downloadDocument(ctx context.Context, documentID, documentKey string) (*provider.File, error) {
	type bodyType struct {
		DocumentKey string `json:"document_key"`
	}

	body := bodyType{
		DocumentKey: documentKey,
	}

	u, _ := url.JoinPath(c.url, "/v2/document/"+documentID+"/result")
	r, _ := http.NewRequestWithContext(ctx, "POST", u, jsonReader(body))
	r.Header.Add("Authorization", "DeepL-Auth-Key "+c.token)
	r.Header.Add("Content-Type", "application/json")

	resp, err := c.client.Do(r)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	content, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	return &provider.File{
		Content:     content,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}
