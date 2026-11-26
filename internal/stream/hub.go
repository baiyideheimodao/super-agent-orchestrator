// Package stream provides a small in-process event hub for SSE clients. Every
// event gets a monotonically increasing sequence number so the browser can
// detect gaps and resume with Last-Event-ID after a reconnect.
package stream

import (
	"context"
	"sync"
	"time"
)

const historyLimit = 4096

type Event struct {
	Seq     int64  `json:"seq"`
	Type    string `json:"type"`
	RunID   string `json:"run_id,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
	At      string `json:"at"`
	Data    any    `json:"data,omitempty"`
}

type Hub struct {
	mu      sync.Mutex
	seq     int64
	history []Event
	clients map[int64]chan Event
	nextID  int64
	once    sync.Once
}

func NewHub() *Hub {
	return &Hub{clients: make(map[int64]chan Event)}
}

// Start emits one tick per interval. Calling Start more than once is safe; the
// first call wins.
func (h *Hub) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	h.once.Do(func() {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					h.mu.Lock()
					for id, ch := range h.clients {
						close(ch)
						delete(h.clients, id)
					}
					h.mu.Unlock()
					return
				case <-ticker.C:
					h.Publish("tick", "", "", map[string]any{"interval_ms": interval.Milliseconds()})
				}
			}
		}()
	})
}

func (h *Hub) Publish(eventType, runID, agentID string, data any) Event {
	h.mu.Lock()
	h.seq++
	event := Event{
		Seq:     h.seq,
		Type:    eventType,
		RunID:   runID,
		AgentID: agentID,
		At:      time.Now().UTC().Format(time.RFC3339Nano),
		Data:    data,
	}
	h.history = append(h.history, event)
	if len(h.history) > historyLimit {
		h.history = append([]Event(nil), h.history[len(h.history)-historyLimit:]...)
	}
	for id, ch := range h.clients {
		select {
		case ch <- event:
		default:
			// A client that cannot keep up is disconnected rather than allowed
			// to silently miss orchestration events. It can reconnect and replay
			// from Last-Event-ID.
			close(ch)
			delete(h.clients, id)
		}
	}
	h.mu.Unlock()
	return event
}

// Subscribe registers a client and returns all retained events after afterSeq.
// The replay slice and the channel are captured under one lock, so no event can
// fall between replay and live delivery.
func (h *Hub) Subscribe(afterSeq int64) (<-chan Event, []Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	id := h.nextID
	h.nextID++
	ch := make(chan Event, 256)
	h.clients[id] = ch

	replay := make([]Event, 0)
	for _, event := range h.history {
		if event.Seq > afterSeq {
			replay = append(replay, event)
		}
	}
	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if existing, ok := h.clients[id]; ok {
			close(existing)
			delete(h.clients, id)
		}
	}
	return ch, replay, cancel
}

func (h *Hub) CurrentSeq() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}
