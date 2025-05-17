package custom

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ provider.Completer = (*Completer)(nil)
)

type Completer struct {
	*Config

	url string

	client CompleterClient
}

func NewCompleter(url string, options ...Option) (*Completer, error) {
	if url == "" || !strings.HasPrefix(url, "grpc://") {
		return nil, errors.New("invalid url")
	}

	cfg := &Config{}

	for _, option := range options {
		option(cfg)
	}

	c := &Completer{
		Config: cfg,

		url: url,
	}

	url = strings.TrimPrefix(c.url, "grpc://")

	conn, err := grpc.Dial(url,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)

	if err != nil {
		return nil, err
	}

	c.client = NewCompleterClient(conn)

	return c, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req := &CompleteRequest{
		Tools:    wireTools(options.Tools),
		Messages: wireMessages(messages),
	}

	if options.Effort != "" {
		val := string(options.Effort)
		req.Effort = &val
	}

	if options.MaxTokens != nil {
		val := int32(*options.MaxTokens)
		req.MaxTokens = &val
	}

	if options.Temperature != nil {
		val := *options.Temperature
		req.Temperature = &val
	}

	if len(options.Stop) > 0 {
		req.Stops = options.Stop
	}

	if options.Format != "" {
		val := string(options.Format)
		req.Format = &val
	}

	if options.Schema != nil {
		req.Schema = &Schema{
			Name:        options.Schema.Name,
			Description: options.Schema.Description,
		}

		if len(options.Schema.Schema) > 0 {
			data, _ := json.Marshal(options.Schema.Schema)
			req.Schema.Properties = string(data)
		} else {
			req.Schema.Properties = "{}"
		}
	}

	stream, err := c.client.Complete(ctx, req)

	if err != nil {
		return nil, err
	}

	for {
		in, err := stream.Recv()

		if err != nil {
			return nil, err
		}

		if in.Delta != nil {
			if options.Stream != nil {
				completion := unwireCompletion(in)

				if err := options.Stream(ctx, completion); err != nil {
					return nil, err
				}
			}
		}

		if in.Message != nil {
			completion := unwireCompletion(in)
			return &completion, nil
		}
	}
}

func wireTools(tools []provider.Tool) []*Tool {
	var result []*Tool

	for _, tool := range tools {
		result = append(result, wireTool(tool))
	}

	return result
}

func wireTool(val provider.Tool) *Tool {
	t := &Tool{
		Name:        val.Name,
		Description: val.Description,
	}

	if len(val.Parameters) > 0 {
		data, _ := json.Marshal(val.Parameters)
		t.Properties = string(data)
	} else {
		t.Properties = "{}"
	}

	return t
}

func wireMessages(messages []provider.Message) []*Message {
	var result []*Message

	for _, message := range messages {
		result = append(result, wireMessage(message))
	}

	return result
}

func wireMessage(val provider.Message) *Message {
	m := &Message{
		Role: string(val.Role),
	}

	for _, c := range val.Content {
		content := &Content{}

		if c.Text != "" {
			content.Text = &c.Text
		}

		if c.Refusal != "" {
			content.Refusal = &c.Refusal
		}

		if c.File != nil {
			content.File = &File{
				Name: c.File.Name,

				Content:     c.File.Content,
				ContentType: c.File.ContentType,
			}
		}

		if c.ToolCall != nil {
			content.ToolCall = &ToolCall{
				Id: c.ToolCall.ID,

				Name:      c.ToolCall.Name,
				Arguments: c.ToolCall.Arguments,
			}
		}

		if c.ToolResult != nil {
			content.ToolResult = &ToolResult{
				Id: c.ToolResult.ID,

				Data: c.ToolResult.Data,
			}
		}

		m.Content = append(m.Content, content)
	}

	return m
}

func unwireCompletion(val *Completion) provider.Completion {
	result := provider.Completion{
		ID:    val.Id,
		Model: val.Model,
	}

	if val.Reason != nil {
		result.Reason = provider.CompletionReason(*val.Reason)
	}

	m := val.Message

	if m == nil {
		m = val.Delta
	}

	if m != nil {
		result.Message = &provider.Message{
			Role: provider.MessageRole(m.Role),
		}

		for _, c := range m.Content {
			content := provider.Content{}

			if c.Text != nil {
				content.Text = *c.Text
			}

			if c.Refusal != nil {
				content.Refusal = *c.Refusal
			}

			if c.File != nil {
				content.File = &provider.File{
					Name: c.File.Name,

					Content:     c.File.Content,
					ContentType: c.File.ContentType,
				}
			}

			if c.ToolCall != nil {
				content.ToolCall = &provider.ToolCall{
					ID: c.ToolCall.Id,

					Name:      c.ToolCall.Name,
					Arguments: c.ToolCall.Arguments,
				}
			}

			if c.ToolResult != nil {
				content.ToolResult = &provider.ToolResult{
					ID: c.ToolResult.Id,

					Data: c.ToolResult.Data,
				}
			}

			result.Message.Content = append(result.Message.Content, content)
		}
	}

	if val.Usage != nil {
		result.Usage = &provider.Usage{
			InputTokens:  int(val.Usage.InputTokens),
			OutputTokens: int(val.Usage.OutputTokens),
		}
	}

	return result
}
