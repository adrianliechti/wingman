package agent

import (
	"github.com/adrianliechti/wingman/pkg/provider"
)

type Agent interface {
	provider.Completer
}
