package policy

import (
	"context"
	"errors"
)

var ErrAccessDenied = errors.New("access denied")

type Resource string

const (
	ResourceModel Resource = "model"
)

type Action string

const (
	ActionAccess Action = "access"
)

type Provider interface {
	Verify(ctx context.Context, resource Resource, id string, action Action) error
}
