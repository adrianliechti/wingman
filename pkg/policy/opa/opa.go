package opa

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/policy"
)

func evalInput(ctx context.Context, resource policy.Resource, id string, action policy.Action) map[string]any {
	user, _ := ctx.Value(auth.UserContextKey).(string)
	email, _ := ctx.Value(auth.EmailContextKey).(string)
	groups, _ := ctx.Value(auth.GroupsContextKey).([]string)

	if groups == nil {
		groups = []string{}
	}

	return map[string]any{
		"resource": resource,
		"id":       id,
		"action":   action,

		"user":   user,
		"email":  email,
		"groups": groups,
	}
}
