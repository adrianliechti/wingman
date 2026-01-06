package router

import (
	"github.com/adrianliechti/wingman/pkg/provider"
)

type Route struct {
	Name        string
	Description string

	Completer          provider.Completer
	CompleterEffort    provider.Effort
	CompleterVerbosity provider.Verbosity
}
