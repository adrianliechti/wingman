package translator

import "fmt"

// Support distinguishes confirmed support from an unknown remote service mode.
// The zero value means unsupported.
type Support uint8

const (
	Unsupported Support = iota
	Supported
	Unknown
)

// MaySupport permits an attempt when support is confirmed or unknown. An
// unknown mode must still be validated by the provider during the request.
func (s Support) MaySupport() bool {
	return s == Supported || s == Unknown
}

func (s Support) String() string {
	switch s {
	case Unsupported:
		return "unsupported"
	case Supported:
		return "supported"
	case Unknown:
		return "unknown"
	default:
		return fmt.Sprintf("invalid(%d)", s)
	}
}

// Capabilities describes the input and output modes implemented by a provider.
// File formats, authentication and model restrictions are still validated by
// the provider when translating.
type Capabilities struct {
	TextToText     Support
	FileToText     Support
	FileToDocument Support
}
