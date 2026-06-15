package classifier

import (
	"hash/fnv"
	"strconv"
)

// fingerprint derives a stable cache key from the routing-relevant signals,
// keyed on the last real user instruction rather than the full message history.
// Within one task the tool round-trips append only assistant tool calls and
// (text-less) tool results, so the instruction — and thus the fingerprint —
// stays constant and the decision is reused; a new user turn changes it and the
// request re-routes.
func fingerprint(s signals) uint64 {
	h := fnv.New64a()

	h.Write([]byte(s.queryText))
	h.Write([]byte{0})

	if s.hasImage {
		h.Write([]byte{'i'})
	}

	h.Write([]byte(strconv.Itoa(s.toolCount)))
	h.Write([]byte{0})

	h.Write([]byte(s.reasoningEffort))

	return h.Sum64()
}
