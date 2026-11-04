package anthropic

import (
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// Keep the native wire variant inside the opaque continuation value. The shared
// Compaction and the Responses API's encrypted_content need no Claude fields.
const compactionSignaturePrefix = "wm:claude-compaction:"

func WrapCompactionSignature(value string) string {
	if value == "" {
		return ""
	}
	realm, raw := provider.ParseSignature(value)
	return provider.TagSignature(realm, compactionSignaturePrefix+raw)
}

func UnwrapCompactionSignature(value string) (string, bool) {
	realm, raw := provider.ParseSignature(value)
	signature, ok := strings.CutPrefix(raw, compactionSignaturePrefix)
	if !ok {
		return value, false
	}
	return provider.TagSignature(realm, signature), true
}

func compactionParam(compaction *provider.Compaction) (anthropic.BetaContentBlockParamUnion, bool) {
	block := &anthropic.BetaCompactionBlockParam{}
	if compaction.Content != "" {
		block.Content = anthropic.String(compaction.Content)
	}
	signature, signed := UnwrapCompactionSignature(compaction.Signature)
	if signed {
		block.Signature = anthropic.String(signature)
	} else if signature != "" {
		block.EncryptedContent = anthropic.String(signature)
	}
	return anthropic.BetaContentBlockParamUnion{OfCompaction: block}, signed
}
