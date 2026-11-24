// Package stream adapts provider deltas for block-oriented streaming protocols.
package stream

import "github.com/adrianliechti/wingman/pkg/provider"

// ReasoningBuffer keeps identified reasoning in replay order. A provider can
// first supply its signature in a terminal snapshot. Visible deltas of the
// first open item can stream immediately, but the next block must wait.
type ReasoningBuffer struct {
	queue    []*block
	unsigned map[string]*block
	final    map[string]bool
}

type block struct {
	parts   []provider.Content
	waiting bool
	hasText bool
}

func (b *ReasoningBuffer) Add(c provider.Completion) []provider.Content {
	var parts []provider.Content
	if c.Message != nil {
		parts = c.Message.Content
	}
	var ready []provider.Content
	for _, p := range parts {
		r := p.Reasoning
		if r != nil && r.ID != "" {
			item := b.unsigned[r.ID]
			if item == nil && r.Signature == "" && !b.final[r.ID] {
				item = &block{waiting: true}
				b.queue = append(b.queue, item)
				if b.unsigned == nil {
					b.unsigned = make(map[string]*block)
				}
				b.unsigned[r.ID] = item
			}
			if r.Signature != "" {
				b.release(r.ID)
			}
			if item != nil {
				item.parts = append(item.parts, p)
				item.hasText = item.hasText || r.Text() != "" || r.SummaryText() != ""
				continue
			}
		}
		if len(b.queue) == 0 {
			ready = append(ready, p)
			continue
		}
		tail := b.queue[len(b.queue)-1]
		if tail.waiting {
			tail = &block{}
			b.queue = append(b.queue, tail)
		}
		tail.parts = append(tail.parts, p)
	}
	for id, snapshot := range c.ReasoningSnapshots {
		if item := b.unsigned[id]; item != nil {
			// A placeholder may have had no visible deltas before its final copy.
			snapshot.ID = id
			if item.hasText {
				snapshot.Content, snapshot.Summary = nil, nil
			}
			if snapshot.Signature != "" || len(snapshot.Content) > 0 || len(snapshot.Summary) > 0 {
				item.parts = append(item.parts, provider.ReasoningContent(snapshot))
			}
			b.release(id)
		}
	}
	return b.drain(ready)
}

// release marks an item final: later deltas with its ID stream through.
func (b *ReasoningBuffer) release(id string) {
	if item := b.unsigned[id]; item != nil {
		item.waiting = false
		delete(b.unsigned, id)
	}
	if b.final == nil {
		b.final = make(map[string]bool)
	}
	b.final[id] = true
}

// Finish releases any remaining content when the stream ends without a signature.
func (b *ReasoningBuffer) Finish() []provider.Content {
	var ready []provider.Content
	for _, item := range b.queue {
		ready = append(ready, item.parts...)
	}
	*b = ReasoningBuffer{}
	return ready
}

func (b *ReasoningBuffer) drain(ready []provider.Content) []provider.Content {
	for len(b.queue) > 0 {
		head := b.queue[0]
		ready = append(ready, head.parts...)
		head.parts = nil
		if head.waiting {
			break
		}
		b.queue[0] = nil
		b.queue = b.queue[1:]
	}
	return ready
}
