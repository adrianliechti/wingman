package codex

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// sessionStore caches codex thread_ids keyed by a canonical hash of the
// conversation prefix, so the next Complete() call can re-attach via
// thread/resume instead of re-priming the whole conversation.
type sessionStore struct {
	mu  sync.Mutex
	cap int
	ttl time.Duration

	lru *list.List
	idx map[string]*list.Element
}

type sessionEntry struct {
	key      string
	threadID string
	created  time.Time
	used     time.Time
}

func newSessionStore(capacity int, ttl time.Duration) *sessionStore {
	if capacity <= 0 {
		capacity = 256
	}
	return &sessionStore{
		cap: capacity,
		ttl: ttl,
		lru: list.New(),
		idx: make(map[string]*list.Element, capacity),
	}
}

func (s *sessionStore) get(key string) (string, bool) {
	if s == nil || key == "" {
		return "", false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	elem, ok := s.idx[key]
	if !ok {
		return "", false
	}

	entry := elem.Value.(*sessionEntry)

	if s.ttl > 0 && time.Since(entry.used) > s.ttl {
		s.lru.Remove(elem)
		delete(s.idx, key)
		return "", false
	}

	entry.used = time.Now()
	s.lru.MoveToFront(elem)
	return entry.threadID, true
}

func (s *sessionStore) put(key, threadID string) {
	if s == nil || key == "" || threadID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if elem, ok := s.idx[key]; ok {
		entry := elem.Value.(*sessionEntry)
		entry.threadID = threadID
		entry.used = time.Now()
		s.lru.MoveToFront(elem)
		return
	}

	entry := &sessionEntry{
		key:      key,
		threadID: threadID,
		created:  time.Now(),
		used:     time.Now(),
	}
	s.idx[key] = s.lru.PushFront(entry)

	for s.lru.Len() > s.cap {
		oldest := s.lru.Back()
		if oldest == nil {
			break
		}
		s.lru.Remove(oldest)
		delete(s.idx, oldest.Value.(*sessionEntry).key)
	}
}

// keyFor hashes the conversation prefix. Mirrors the claude provider:
// only system + user content (text, files, tool_result blocks) is hashed —
// assistant turns are excluded so callers don't break the cache by
// reformatting echoed assistant content.
func keyFor(messages []provider.Message) string {
	h := sha256.New()

	for _, m := range messages {
		if m.Role != provider.MessageRoleSystem && m.Role != provider.MessageRoleUser {
			continue
		}

		_, _ = h.Write([]byte{0x1e})
		_, _ = h.Write([]byte(m.Role))

		for _, c := range m.Content {
			_, _ = h.Write([]byte{0x1f})

			if c.Text != "" {
				_, _ = h.Write([]byte("t:"))
				_, _ = h.Write([]byte(c.Text))
			}
			if c.ToolResult != nil {
				_, _ = h.Write([]byte("u:"))
				_, _ = h.Write([]byte(c.ToolResult.ID))
				for _, p := range c.ToolResult.Parts {
					if p.Text != "" {
						_, _ = h.Write([]byte("t:"))
						_, _ = h.Write([]byte(p.Text))
					}
					if p.File != nil {
						_, _ = h.Write([]byte("f:"))
						fileHash := sha256.Sum256(p.File.Content)
						_, _ = h.Write(fileHash[:])
						_, _ = h.Write([]byte(p.File.ContentType))
					}
				}
			}
			if c.File != nil {
				_, _ = h.Write([]byte("f:"))
				fileHash := sha256.Sum256(c.File.Content)
				_, _ = h.Write(fileHash[:])
				_, _ = h.Write([]byte(c.File.ContentType))
			}
		}
	}

	return hex.EncodeToString(h.Sum(nil))
}
