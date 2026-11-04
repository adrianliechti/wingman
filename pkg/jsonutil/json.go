// Package jsonutil provides lossless decoding for opaque JSON carried between
// clients, providers and tools. Typed protocol fields keep their normal types.
package jsonutil

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
)

// PreserveNumbers keeps opaque JSON numbers as Number instead of float64.
// Streaming custom decoders inherit this option.
var PreserveNumbers = json.WithUnmarshalers(json.UnmarshalFromFunc(func(dec *jsontext.Decoder, value *any) error {
	if dec.PeekKind() != '0' {
		return errors.ErrUnsupported
	}
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	*value = Number(string(raw))
	return nil
}))

// Unmarshal decodes one complete JSON value with lossless opaque numbers.
// Like json.Unmarshal, it may partially populate value before reporting an error.
func Unmarshal(data []byte, value any, opts ...json.Options) error {
	return json.Unmarshal(data, value, PreserveNumbers, json.JoinOptions(opts...))
}

// UnmarshalRead decodes a single JSON value and rejects trailing input.
func UnmarshalRead(reader io.Reader, value any, opts ...json.Options) error {
	return json.UnmarshalRead(reader, value, PreserveNumbers, json.JoinOptions(opts...))
}
