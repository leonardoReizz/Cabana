// Package logstream fans log lines out to any number of live SSE subscribers.
//
// It exists because two very different producers write to the same stream: the
// build Job's pod logs, which only exist during a deploy, and the running app's
// pod logs, which outlive it. Subscribers should not care which is which, and a
// dashboard open during a deploy should see the build scroll past and the app
// come up without reconnecting.
package logstream

import (
	"sync"
	"time"
)

type Line struct {
	Timestamp time.Time `json:"ts"`
	Source    string    `json:"source"` // "build" | "app" | "cabana"
	Text      string    `json:"text"`
}

// ringSize caps the replay buffer per app. A subscriber that connects mid-build
// gets the recent history rather than joining blind, but memory stays bounded
// no matter how chatty an app is.
const ringSize = 200

type subscriber struct {
	ch chan Line
}

type topic struct {
	mu      sync.Mutex
	ring    []Line
	subs    map[*subscriber]struct{}
}

type Hub struct {
	mu     sync.RWMutex
	topics map[string]*topic
}

func New() *Hub {
	return &Hub{topics: make(map[string]*topic)}
}

func (h *Hub) topicFor(key string) *topic {
	h.mu.RLock()
	t, ok := h.topics[key]
	h.mu.RUnlock()
	if ok {
		return t
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	// Re-check: another goroutine may have created it between the two locks.
	if t, ok := h.topics[key]; ok {
		return t
	}
	t = &topic{subs: make(map[*subscriber]struct{})}
	h.topics[key] = t
	return t
}

// Publish appends a line to the replay ring and delivers it to every live
// subscriber. Delivery is non-blocking: a subscriber that cannot keep up drops
// lines rather than stalling the producer, because a wedged browser tab must
// never be able to block a build.
func (h *Hub) Publish(key string, line Line) {
	t := h.topicFor(key)
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ring = append(t.ring, line)
	if len(t.ring) > ringSize {
		t.ring = t.ring[len(t.ring)-ringSize:]
	}

	for sub := range t.subs {
		select {
		case sub.ch <- line:
		default:
		}
	}
}

// Subscribe returns a channel of lines, primed with the replay buffer, and a
// cancel function the caller must invoke to release the subscription.
func (h *Hub) Subscribe(key string) (<-chan Line, func()) {
	t := h.topicFor(key)
	sub := &subscriber{ch: make(chan Line, ringSize+64)}

	t.mu.Lock()
	for _, line := range t.ring {
		// Buffer is sized to hold the whole replay, so this cannot block.
		sub.ch <- line
	}
	t.subs[sub] = struct{}{}
	t.mu.Unlock()

	return sub.ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, ok := t.subs[sub]; ok {
			delete(t.subs, sub)
			close(sub.ch)
		}
	}
}

// Drop discards a topic entirely. Called when an app is deleted so its buffered
// logs do not outlive it.
func (h *Hub) Drop(key string) {
	h.mu.Lock()
	t, ok := h.topics[key]
	delete(h.topics, key)
	h.mu.Unlock()
	if !ok {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	for sub := range t.subs {
		delete(t.subs, sub)
		close(sub.ch)
	}
	t.ring = nil
}
