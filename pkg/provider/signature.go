package provider

import "strings"

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
	if !ok || realm == "" {
		return "", value
	}

	return realm, signature
}
