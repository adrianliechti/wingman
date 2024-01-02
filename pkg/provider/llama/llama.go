package llama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/adrianliechti/llama/pkg/provider"
	"github.com/adrianliechti/llama/pkg/provider/llama/grammar"
	"github.com/adrianliechti/llama/pkg/provider/llama/prompt"

	"github.com/google/uuid"
)

var (
	_ provider.Provider = &Provider{}
)

type Provider struct {
	url string

	client *http.Client

	system   string
	template prompt.Template
}

type Option func(*Provider)

type Template = prompt.Template

var (
	TemplateChatML     = prompt.ChatML
	TemplateLlama      = prompt.Llama
	TemplateLlamaGuard = prompt.LlamaGuard
	TemplateMistral    = prompt.Mistral
)

func New(url string, options ...Option) (*Provider, error) {
	p := &Provider{
		url: url,

		client: http.DefaultClient,

		system:   "",
		template: prompt.Llama,
	}

	for _, option := range options {
		option(p)
	}

	if p.url == "" {
		return nil, errors.New("invalid url")
	}

	return p, nil
}

func WithClient(client *http.Client) Option {
	return func(p *Provider) {
		p.client = client
	}
}

func WithSystem(system string) Option {
	return func(p *Provider) {
		p.system = system
	}
}

func WithTemplate(template Template) Option {
	return func(p *Provider) {
		p.template = template
	}
}

func (p *Provider) Embed(ctx context.Context, model, content string) ([]float32, error) {
	body := &EmbeddingRequest{
		Content: strings.TrimSpace(content),
	}

	u, _ := url.JoinPath(p.url, "/embedding")
	resp, err := p.client.Post(u, "application/json", jsonReader(body))

	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("unable to embed")
	}

	defer resp.Body.Close()

	var result EmbeddingResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Embedding, nil
}

func (p *Provider) Complete(ctx context.Context, model string, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	if options == nil {
		options = &provider.CompleteOptions{}
	}

	id := uuid.NewString()

	url, _ := url.JoinPath(p.url, "/completion")
	body, err := p.convertCompletionRequest(messages, options)

	if err != nil {
		return nil, err
	}

	println(body.Prompt)

	if options.Stream == nil {
		resp, err := p.client.Post(url, "application/json", jsonReader(body))

		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			return nil, errors.New("unable to complete")
		}

		defer resp.Body.Close()

		var completion CompletionResponse

		if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
			return nil, err
		}

		content := strings.TrimSpace(completion.Content)
		println(content)

		var resultRole = provider.MessageRoleAssistant
		var resultReason = toCompletionReason(completion)

		result := provider.Completion{
			ID: id,

			Reason: resultReason,

			Message: provider.Message{
				Role:    resultRole,
				Content: content,
			},
		}

		type functionScheme struct {
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
		}

		var function functionScheme

		if err := json.Unmarshal([]byte(content), &function); err == nil {
			result.Reason = provider.CompletionReasonFunction
			result.Message.Content = ""

			args, _ := json.Marshal(function.Arguments)

			result.Message.FunctionCalls = []provider.FunctionCall{
				{
					ID: uuid.NewString(),

					Name:      function.Name,
					Arguments: string(args),
				},
			}
		}

		return &result, nil
	} else {
		defer close(options.Stream)

		req, _ := http.NewRequestWithContext(ctx, "POST", url, jsonReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")

		resp, err := p.client.Do(req)

		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, errors.New("unable to complete")
		}

		reader := bufio.NewReader(resp.Body)

		var resultText strings.Builder
		var resultRole provider.MessageRole
		var resultReason provider.CompletionReason

		for i := 0; ; i++ {
			data, err := reader.ReadBytes('\n')

			if errors.Is(err, io.EOF) {
				break
			}

			if err != nil {
				return nil, err
			}

			data = bytes.TrimSpace(data)

			// if bytes.HasPrefix(data, errorPrefix) {
			// }

			data = bytes.TrimPrefix(data, []byte("data: "))

			if len(data) == 0 {
				continue
			}

			var completion CompletionResponse

			if err := json.Unmarshal([]byte(data), &completion); err != nil {
				return nil, err
			}

			var content = completion.Content

			if i == 0 {
				content = strings.TrimLeftFunc(content, unicode.IsSpace)
			}

			resultText.WriteString(content)

			resultRole = provider.MessageRoleAssistant
			resultReason = toCompletionReason(completion)

			options.Stream <- provider.Completion{
				ID: id,

				Reason: resultReason,

				Message: provider.Message{
					Role:    resultRole,
					Content: content,
				},
			}
		}

		result := provider.Completion{
			ID: id,

			Reason: resultReason,

			Message: provider.Message{
				Role:    resultRole,
				Content: resultText.String(),
			},
		}

		return &result, nil
	}
}

func (p *Provider) convertCompletionRequest(messages []provider.Message, options *provider.CompleteOptions) (*CompletionRequest, error) {
	if options == nil {
		options = &provider.CompleteOptions{}
	}

	system := p.system

	if len(options.Functions) > 0 {
		system = "You have access to the following functions. Use them if required:\n\n"

		type schemaFunctionCall struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Parameters  any    `json:"parameters"`
		}

		type schemaFunction struct {
			Type     string `json:"type"`
			Function any    `json:"function"`
		}

		schema := []schemaFunction{}

		for _, f := range options.Functions {
			schema = append(schema, schemaFunction{
				Type: "function",

				Function: schemaFunctionCall{
					Name:        f.Name,
					Description: f.Description,
					Parameters:  f.Parameters,
				},
			})
		}

		data, _ := json.MarshalIndent(schema, "", "  ")
		system += string(data)
	}

	for i, m := range messages {
		if m.Role == provider.MessageRoleFunction {
			content := "Use this information to answer the following question: " + m.Content + "\n\n"
			content += messages[i-2].Content

			m.Role = provider.MessageRoleUser
			m.Content = content
			messages[i] = m
		}
	}

	prompt, err := p.template.Prompt(system, messages)

	if err != nil {
		return nil, err
	}

	req := &CompletionRequest{
		Prompt: prompt,

		Stream: options.Stream != nil,

		Temperature: options.Temperature,
		TopP:        options.TopP,
		MinP:        options.MinP,

		Stop: p.template.Stop(),
	}

	if options.Format == provider.CompletionFormatJSON {
		req.Grammar = grammar.JSON
	}

	return req, nil
}

func toCompletionReason(resp CompletionResponse) provider.CompletionReason {
	if resp.Truncated {
		return provider.CompletionReasonLength
	}

	if resp.Stop {
		return provider.CompletionReasonStop
	}

	return ""
}

func jsonReader(v any) io.Reader {
	b := new(bytes.Buffer)

	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)

	enc.Encode(v)
	return b
}
