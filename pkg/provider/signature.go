package provider

import (
	"encoding/base64"
	"encoding/json/v2"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Reasoning and compaction signatures (Anthropic thinking signatures, OpenAI
// encrypted reasoning, Gemini thought signatures) are opaque blobs that only
// the backend which produced them can verify — and "backend" is as narrow as
// one model behind one set of credentials. Wire formats give them no origin,
// so signatures are tagged with the realm that produced them on the way out
// and only replayed into the same realm (see adapter/signatures).
//
// Tagged form: "@<realm>:<signature>". Untagged values predate tagging and
// are treated as the replaying realm's own.
const signatureTagPrefix = "@"

// TagSignature prefixes a raw signature with its realm. Empty signatures
// stay empty.
func TagSignature(realm, signature string) string {
	if signature == "" || realm == "" {
		return signature
	}

	return signatureTagPrefix + realm + ":" + signature
}

// ParseSignature splits a tagged signature into realm and raw value. An
// untagged value comes back with an empty realm.
func ParseSignature(value string) (realm, signature string) {
	raw, ok := strings.CutPrefix(value, signatureTagPrefix)
	if !ok {
		return "", value
	}

	realm, signature, ok = strings.Cut(raw, ":")
	if !ok || !validSignatureRealm(realm) || !validSignaturePayload(signature) {
		return "", value
	}

	return realm, signature
}

func validSignatureRealm(realm string) bool {
	if realm == "" || !utf8.ValidString(realm) {
		return false
	}

	// Model aliases may contain spaces and Unicode; binary signatures must
	// still be distinguished from realm tags.
	for _, r := range realm {
		if !unicode.IsPrint(r) {
			return false
		}
	}

	return true
}

func validSignaturePayload(signature string) bool {
	if signature == "" {
		return false
	}

	for i := 0; i < len(signature); i++ {
		if signature[i] < ' ' || signature[i] > '~' {
			return false
		}
	}

	return true
}

// Anthropic thinking blocks and Gemini thought signatures have no item ID
// field. Those surfaces carry the original item ID inside the signature
// so that Responses clients can replay the full item. Values without the prefix are
// signatures as the backend produced them.
const reasoningIDPrefix = "wm:"

type reasoningIDSignature struct {
	ID        string `json:"id"`
	Signature string `json:"s"`
}

// WrapReasoningID stores the reasoning item ID inside its signature and keeps
// the outer realm tag.
func WrapReasoningID(id, signature string) string {
	if id == "" || signature == "" {
		return signature
	}

	realm, raw := ParseSignature(signature)
	data, err := json.Marshal(reasoningIDSignature{ID: id, Signature: raw})
	if err != nil {
		return signature
	}

	return TagSignature(realm, reasoningIDPrefix+base64.RawURLEncoding.EncodeToString(data))
}

// UnwrapReasoningID reverses WrapReasoningID. Other values come back unchanged
// with an empty ID.
func UnwrapReasoningID(value string) (id, signature string) {
	realm, raw := ParseSignature(value)
	encoded, ok := strings.CutPrefix(raw, reasoningIDPrefix)

	if !ok {
		return "", value
	}

	data, err := base64.RawURLEncoding.DecodeString(encoded)

	if err != nil {
		return "", value
	}

	var w reasoningIDSignature

	if err := json.Unmarshal(data, &w); err != nil || w.ID == "" || w.Signature == "" {
		return "", value
	}

	return w.ID, TagSignature(realm, w.Signature)
}
