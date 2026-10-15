package otel

import (
	"os"
)

const instrumentationName = "github.com/adrianliechti/wingman"

var (
	EnableDebug     = false
	EnableTelemetry = false
)

func init() {
	EnableDebug = os.Getenv("DEBUG") != ""
	EnableTelemetry = os.Getenv("TELEMETRY") != ""
}

type Observable interface {
	otelSetup()
}

func Instrumented[T any](p T) bool {
	for {
		if _, ok := any(p).(Observable); ok {
			return true
		}

		u, ok := any(p).(interface{ Unwrap() T })

		if !ok {
			return false
		}

		p = u.Unwrap()
	}
}
