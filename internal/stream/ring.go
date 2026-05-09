package stream

import (
	"sync"
	"time"

	"ptrack/internal/model"
)

type Ring struct {
	mu            sync.RWMutex
	limitBytes    int
	retainedBytes int
	truncated     bool
	lastSeq       int64
	entries       []model.LogEntry
}

func NewRing(limitBytes int) *Ring {
	if limitBytes <= 0 {
		limitBytes = 64 * 1024
	}
	return &Ring{limitBytes: limitBytes, entries: make([]model.LogEntry, 0, 32)}
}

func (r *Ring) Append(streamName, text string, now time.Time) model.LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lastSeq++
	entry := model.LogEntry{
		Seq:       r.lastSeq,
		Timestamp: now.UTC(),
		Stream:    streamName,
		Text:      text,
	}
	r.entries = append(r.entries, entry)
	r.retainedBytes += len(entry.Text)

	for r.retainedBytes > r.limitBytes && len(r.entries) > 0 {
		r.truncated = true
		r.retainedBytes -= len(r.entries[0].Text)
		r.entries = append([]model.LogEntry(nil), r.entries[1:]...)
	}

	return entry
}

func (r *Ring) Entries(after int64, limit int) ([]model.LogEntry, int64) {
	if limit <= 0 {
		limit = 500
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	items := make([]model.LogEntry, 0, limit)
	nextAfter := after
	for _, entry := range r.entries {
		if entry.Seq <= after {
			continue
		}
		items = append(items, entry)
		nextAfter = entry.Seq
		if len(items) >= limit {
			break
		}
	}

	return append([]model.LogEntry(nil), items...), nextAfter
}

func (r *Ring) Stats() model.LogRetention {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return model.LogRetention{
		RetainedBytes: r.retainedBytes,
		Truncated:     r.truncated,
		LastSeq:       r.lastSeq,
	}
}
