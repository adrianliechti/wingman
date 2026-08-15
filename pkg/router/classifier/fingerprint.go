package classifier

import (
	"hash/fnv"
	"strconv"
)

// fingerprint derives a stable cache key for an explicit affinity identity.
// It deliberately ignores evolving history signals when identity is non-empty:
// changing tool history does not re-run an expensive judge, though hard
// eligibility constraints are still checked on every cache hit.
func fingerprint(s signals, identity string) uint64 {
	h := fnv.New64a()

	h.Write([]byte(identity))
	h.Write([]byte{0})
	h.Write([]byte(s.queryText))
	h.Write([]byte{0})

	if s.hasImage {
		h.Write([]byte{'i'})
	}
	if s.hasNonImageFile {
		h.Write([]byte{'f'})
	}

	h.Write([]byte(strconv.Itoa(s.toolCount)))
	h.Write([]byte{0})
	h.Write([]byte(s.reasoningEffort))

	if identity == "" {
		// This branch is only useful to tests and callers that need a complete
		// request fingerprint. classify never caches an unidentified request.
		for _, value := range []int{
			s.approxTokens,
			s.taskTokens,
			s.recentFences,
			s.historyFences,
			s.recentHard,
			s.historyHard,
		} {
			h.Write([]byte{0})
			h.Write([]byte(strconv.Itoa(value)))
		}

		if s.escalate {
			h.Write([]byte("\x00escalate"))
		}
		if s.deescalate {
			h.Write([]byte("\x00deescalate"))
		}
	}

	return h.Sum64()
}
