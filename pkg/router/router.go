package router

import (
	"github.com/adrianliechti/wingman/pkg/provider"
)

type Route struct {
	Name        string
	Description string

	Completer provider.Completer

	Options *RouteOptions
}

type RouteOptions struct {
	Effort    provider.Effort
	Verbosity provider.Verbosity
}
