package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/adrianliechti/wingman/pkg/translator"

	"cloud.google.com/go/auth"
	cloudtranslate "cloud.google.com/go/translate/apiv3"
	"cloud.google.com/go/translate/apiv3/translatepb"
)

var _ translator.Provider = (*Client)(nil)

type Client struct {
	client *http.Client

	url   string
	token string

	project  string
	location string

	tokenProvider auth.TokenProvider
	credentials   func() (*auth.Credentials, error)

	mutex  sync.Mutex
	sdk    *cloudtranslate.TranslationClient
	parent string
	closed bool
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" {
		url = "https://translate.googleapis.com"
	}

	c := &Client{
		client: http.DefaultClient,
		url:    url,

		location: "global",
	}

	for _, option := range options {
		option(c)
	}

	if err := c.configureCredentials(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Client) Capabilities() translator.Capabilities {
	// Documents use OAuth even when an API key is configured for text.
	return translator.Capabilities{TextToText: translator.Supported, FileToDocument: translator.Supported}
}

func (c *Client) Translate(ctx context.Context, input translator.Input, options *translator.TranslateOptions) (*translator.File, error) {
	c.mutex.Lock()
	closed := c.closed
	c.mutex.Unlock()

	if closed {
		return nil, errors.New("google translator: client is closed")
	}

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
	text := strings.TrimSpace(input)

	if text == "" {
		return nil, errors.New("translator: no content to translate")
	}

	if c.token != "" {
		return c.translateTextAPIKey(ctx, text, language)
	}

	client, parent, err := c.translationClient(ctx)

	if err != nil {
		return nil, err
	}

	result, err := client.TranslateText(ctx, &translatepb.TranslateTextRequest{
		Parent:             parent,
		Contents:           []string{text},
		MimeType:           "text/plain",
		TargetLanguageCode: language,
	})

	if err != nil {
		return nil, err
	}

	translations := result.GetTranslations()

	if len(translations) != 1 || translations[0].GetTranslatedText() == "" {
		return nil, errors.New("unable to translate content")
	}

	return &translator.File{
		Content:     []byte(translations[0].GetTranslatedText()),
		ContentType: "text/plain",
	}, nil
}

func (c *Client) translateTextAPIKey(ctx context.Context, text, language string) (*translator.File, error) {
	body := struct {
		Text   []string `json:"q"`
		Target string   `json:"target"`
		Format string   `json:"format"`
	}{
		Text:   []string{text},
		Target: language,
		Format: "text",
	}

	data, err := json.Marshal(body)

	if err != nil {
		return nil, err
	}

	u, err := url.JoinPath(c.url, "/language/translate/v2")

	if err != nil {
		return nil, err
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))

	if err != nil {
		return nil, err
	}

	r.Header.Set("Content-Type", "application/json")

	r.Header.Set("X-Goog-Api-Key", c.token)

	resp, err := c.client.Do(r)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var result struct {
		Data struct {
			Translations []struct {
				Text string `json:"translatedText"`
			} `json:"translations"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Data.Translations) == 0 || result.Data.Translations[0].Text == "" {
		return nil, errors.New("unable to translate content")
	}

	return &translator.File{
		Content:     []byte(result.Data.Translations[0].Text),
		ContentType: "text/plain",
	}, nil
}
