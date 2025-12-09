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
