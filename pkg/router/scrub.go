package router

import (
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
)

// ScrubMessages removes history a routed request cannot carry across
// members. Reasoning and compaction signatures stay: every member is scoped
// to its own realm, so a signature replays when the router lands on the
// member that produced it and is dropped by any other member. Gemini tool-id
// signatures are not realm-tagged and are removed.
func ScrubMessages(messages []provider.Message) []provider.Message {
	return signatures.StripToolSignatures(messages)
}

// ScrubOptions disables compaction for a routed request: the next turn may
// land on another member, which cannot resume the compacted history from a
// foreign blob.
func ScrubOptions(options *provider.CompleteOptions) *provider.CompleteOptions {
	if options == nil || options.CompactionOptions == nil {
		return options
	}

	cloned := *options
	cloned.CompactionOptions = nil

	return &cloned
}
