package anthropic

import (
	"encoding/base64"
	"encoding/json/v2"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
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

	realm, raw := provider.ParseSignature(signature)
	data, _ := json.Marshal(wrappedSignature{ID: id, Signature: raw})

	return provider.TagSignature(realm, signaturePrefix+base64.RawURLEncoding.EncodeToString(data))
}

func decodeSignature(value string) (id, signature string) {
	realm, raw := provider.ParseSignature(value)
	encoded, ok := strings.CutPrefix(raw, signaturePrefix)

	if !ok {
		return "", value
	}

	data, err := base64.RawURLEncoding.DecodeString(encoded)

	if err != nil {
		return "", value
	}

	var w wrappedSignature

	if err := json.Unmarshal(data, &w); err != nil || w.ID == "" || w.Signature == "" {
		return "", value
	}

	return w.ID, provider.TagSignature(realm, w.Signature)
}
