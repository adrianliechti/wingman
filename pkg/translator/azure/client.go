package azure

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/translator"
)

var _ translator.Provider = (*Client)(nil)

type Client struct {
	client *http.Client

	url   string
	token string

	region string
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" {
		url = "https://api.cognitive.microsofttranslator.com"
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

func (c *Client) TranslateCapabilities() translator.Capabilities {
	return translator.Capabilities{TextToText: translator.Supported, FileToDocument: translator.Supported}
}

func (c *Client) Translate(ctx context.Context, input translator.Input, options *translator.TranslateOptions) (*translator.File, error) {
	language := "en"
	if options != nil && options.Language != "" {
		language = options.Language
	}

	if input.File != nil {
		return c.translateFile(ctx, input.File, language)
	}

	return c.translateText(ctx, input.Text, language)
}

func (c *Client) translateText(ctx context.Context, input, language string) (*translator.File, error) {
	type bodyType struct {
		Text string `json:"Text"`
	}

	body := []bodyType{
		{
			Text: strings.TrimSpace(input),
		},
	}

	u, _ := url.Parse(strings.TrimRight(c.url, "/") + "/translator/text/v3.0/translate")

	query := u.Query()
	query.Set("to", language)
	query.Set("api-version", "3.0")

	u.RawQuery = query.Encode()

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	r.Header.Add("Content-Type", "application/json")

	if c.token != "" {
		r.Header.Add("Ocp-Apim-Subscription-Key", c.token)
	}

	if c.region != "" {
		r.Header.Add("Ocp-Apim-Subscription-Region", c.region)
	}

	resp, err := c.client.Do(r)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	type resultType struct {
		DetectedLanguage struct {
			Language string  `json:"language"`
			Score    float64 `json:"score"`
		} `json:"detectedLanguage"`

		Translations []struct {
			Text string `json:"text"`
			To   string `json:"to"`
		} `json:"translations"`
	}

	var result []resultType

	if err := json.UnmarshalRead(resp.Body, &result); err != nil {
		return nil, err
	}

	if len(result) == 0 || len(result[0].Translations) == 0 {
		return nil, errors.New("unable to translate content")
	}

	return &translator.File{
		Content:     []byte(result[0].Translations[0].Text),
		ContentType: "text/plain",
	}, nil
}

func (c *Client) translateFile(ctx context.Context, input *translator.File, language string) (*translator.File, error) {
	u, err := url.Parse(strings.TrimRight(c.url, "/") + "/translator/document:translate")
	if err != nil {
		return nil, err
	}

	query := u.Query()
	query.Set("targetLanguage", language)
	query.Set("api-version", "2024-05-01")

	u.RawQuery = query.Encode()

	r, err := provider.NewMultipartRequest(ctx, u.String(), provider.MultipartPart{
		Header: textproto.MIMEHeader{
			"Content-Disposition": {multipart.FileContentDisposition("document", input.Name)},
			"Content-Type":        {"application/octet-stream"},
		},
		Content: bytes.NewReader(input.Content),
	})
	if err != nil {
		return nil, err
	}

	if c.token != "" {
		r.Header.Add("Ocp-Apim-Subscription-Key", c.token)
	}

	if c.region != "" {
		r.Header.Add("Ocp-Apim-Subscription-Region", c.region)
	}

	resp, err := c.client.Do(r)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	data, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	return &translator.File{
		Name:        input.Name,
		Content:     data,
		ContentType: input.ContentType,
	}, nil
}
