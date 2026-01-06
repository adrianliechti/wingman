package auto

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
	"github.com/adrianliechti/wingman/pkg/template"
)

type Completer struct {
	completer provider.Completer

	routes []router.Route
}

var (
	//go:embed prompt.tmpl
	promptTemplateText string

	promptTemplate *template.Template = template.MustTemplate(promptTemplateText)
)

func NewCompleter(completer provider.Completer, routes ...router.Route) (provider.Completer, error) {
	c := &Completer{
		completer: completer,

		routes: routes,
	}

	return c, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		route, err := c.determineRoute(ctx, messages)

		if err != nil {
			yield(nil, err)
			return
		}

		println("selected route:", route.Name)

		if route.Options != nil {
			if route.Options.Effort != "" {
				options.Effort = route.Options.Effort
			}

			if route.Options.Verbosity != "" {
				options.Verbosity = route.Options.Verbosity
			}
		}

		for completion, err := range route.Completer.Complete(ctx, messages, options) {
			if err != nil {
				println(err.Error())
			}

			if !yield(completion, err) {
				return
			}
		}
	}
}

func (c *Completer) determineRoute(ctx context.Context, candidates []provider.Message) (*router.Route, error) {
	instructions, _ := promptTemplate.Execute(map[string]any{
		"routes": buildRoutesXML(c.routes),
	})

	messages := []provider.Message{
		provider.SystemMessage(instructions),
	}

	for _, m := range candidates {
		if m.Role == provider.MessageRoleSystem {
			continue
		}

		messages = append(messages, m)
	}

	options := &provider.CompleteOptions{
		Schema: &provider.Schema{
			Name: "router_response",

			Schema: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"route": map[string]any{
						"type":        "string",
						"description": "The name of the selected route",
					},
				},

				"required":             []string{"route"},
				"additionalProperties": false,
			},
		},
	}

	acc := provider.CompletionAccumulator{}

	for completion, err := range c.completer.Complete(ctx, messages, options) {
		if err != nil {
			return nil, err
		}

		acc.Add(*completion)
	}

	completion := acc.Result()

	type RouterResponse struct {
		Route string `json:"route"`
	}

	var result RouterResponse

	if err := json.Unmarshal([]byte(completion.Message.Text()), &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	for _, r := range c.routes {
		if strings.EqualFold(r.Name, result.Route) {
			return &r, nil
		}
	}

	if strings.EqualFold(result.Route, "other") && len(c.routes) > 0 {
		return &c.routes[0], nil
	}

	return nil, fmt.Errorf("route %q not found", result.Route)
}

func buildRoutesXML(routes []router.Route) string {
	var sb strings.Builder

	sb.WriteString("<routes>\n")

	for _, route := range routes {
		fmt.Fprintf(&sb, "  <route name=\"%s\">%s</route>\n", route.Name, route.Description)
	}

	sb.WriteString("</routes>")

	return sb.String()
}
