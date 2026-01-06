package auto

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
	"github.com/adrianliechti/wingman/pkg/template"
)

type Completer struct {
	completer provider.Completer

	routes map[string]router.Route
}

var (
	//go:embed prompt.tmpl
	promptTemplateText string

	promptTemplate *template.Template = template.MustTemplate(promptTemplateText)
)

func NewCompleter(completer provider.Completer, routes ...router.Route) (provider.Completer, error) {
	c := &Completer{
		completer: completer,

		routes: make(map[string]router.Route),
	}

	for _, route := range routes {
		c.routes[route.Name] = route
	}

	return c, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		instructions, _ := promptTemplate.Execute(map[string]any{
			"routes": buildRoutesXML(slices.Collect(maps.Values(c.routes))),
		})

		println(instructions)

		yield(nil, errors.ErrUnsupported)

		type RouterResponse struct {
			Route string `json:"route"`
		}
	}
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
