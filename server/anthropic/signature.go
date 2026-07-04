package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// OpenAI-backed reasoning replays only with its original item id, but thinking
// blocks have no id field — carry it inside the signature. Values without the
// prefix are native signatures.
const signaturePrefix = "wm:"

type wrappedSignature struct {
	ID        string `json:"id"`
	Signature string `json:"s"`
}

func encodeSignature(id, signature string) string {
	if id == "" || signature == "" {
		return signature
	}

	data, _ := json.Marshal(wrappedSignature{ID: id, Signature: signature})

	return signaturePrefix + base64.RawURLEncoding.EncodeToString(data)
}

func decodeSignature(value string) (id, signature string) {
	raw, ok := strings.CutPrefix(value, signaturePrefix)

	if !ok {
		return "", value
	}

	data, err := base64.RawURLEncoding.DecodeString(raw)

	if err != nil {
		return "", value
	}

	var w wrappedSignature

	if err := json.Unmarshal(data, &w); err != nil {
		return "", value
	}

	return w.ID, w.Signature
}
