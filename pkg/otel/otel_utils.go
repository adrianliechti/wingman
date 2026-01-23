package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/auth"

	"go.opentelemetry.io/otel/attribute"
)

type KeyValue = attribute.KeyValue

func String(key string, val string) KeyValue {
	return attribute.String(key, val)
}

func Strings(key string, val []string) KeyValue {
	return attribute.StringSlice(key, val)
}

func KeyValues(attrs ...[]KeyValue) []KeyValue {
	var result []KeyValue

	for _, a := range attrs {
		result = append(result, a...)
	}

	return result
}

func EndUserAttrs(ctx context.Context) []KeyValue {
	var attrs []KeyValue

	if user, ok := ctx.Value(auth.UserContextKey).(string); ok && user != "" {
		attrs = append(attrs, attribute.String("enduser.id", user))
	}

	if email, ok := ctx.Value(auth.EmailContextKey).(string); ok && email != "" {
		attrs = append(attrs, attribute.String("enduser.email", email))
	}

	return attrs
}
