package rpc

import (
	"bytes"
	"sync"
)

// SubscriberQueueSize bounds live events waiting for one subscriber.
const SubscriberQueueSize = 1000

// Hub fans out events without waiting for a reader or a network connection.
// A slow subscriber is detached and receives a lagged signal from its sender.
type Hub struct {
	mu   sync.Mutex
	subs map[*Subscription]struct{}
}

func NewHub() *Hub { return &Hub{subs: make(map[*Subscription]struct{})} }

// Subscription is one registered live feed. Close is safe with Publish.
type Subscription struct {
	hub    *Hub
	queue  chan Event
	lagged chan struct{}
	done   chan struct{}
	once   sync.Once
	types  map[string]struct{} // guarded by hub.mu
	chats  map[int64]struct{}  // guarded by hub.mu
}

// Subscribe registers before history is read, so events published during
// replay enter the queue and can be deduplicated by the sender.
func (h *Hub) Subscribe() *Subscription {
	sub := &Subscription{hub: h, queue: make(chan Event, SubscriberQueueSize), lagged: make(chan struct{}), done: make(chan struct{})}
	h.mu.Lock()
	if h.subs == nil {
		h.subs = make(map[*Subscription]struct{})
	}
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

// Publish never blocks on a subscriber's queue. The event data is copied so a
// producer may reuse its buffer after this call.
func (h *Hub) Publish(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		if !sub.matches(event) {
			continue
		}
		select {
		case sub.queue <- cloneEvent(event):
		default:
			close(sub.lagged)
			delete(h.subs, sub)
		}
	}
}

func cloneEvent(event Event) Event {
	event.Data = bytes.Clone(event.Data)
	if event.ChatID != nil {
		id := *event.ChatID
		event.ChatID = &id
	}
	return event
}

// SetFilter installs fixed type and chat sets for future live events.
func (s *Subscription) SetFilter(filter EventFilter) {
	s.hub.mu.Lock()
	if len(filter.Types) > 0 {
		s.types = make(map[string]struct{}, len(filter.Types))
		for _, typ := range filter.Types {
			s.types[typ] = struct{}{}
		}
	}
	if len(filter.ChatIDs) > 0 {
		s.chats = make(map[int64]struct{}, len(filter.ChatIDs))
		for _, id := range filter.ChatIDs {
			s.chats[id] = struct{}{}
		}
	}
	s.hub.mu.Unlock()
}

// matches is called under hub.mu by Publish and by the buffered-event reader.
func (s *Subscription) matches(event Event) bool {
	if len(s.types) > 0 {
		if _, ok := s.types[event.Type]; !ok {
			return false
		}
	}
	if len(s.chats) > 0 {
		if event.ChatID == nil {
			return false
		}
		if _, ok := s.chats[*event.ChatID]; !ok {
			return false
		}
	}
	return true
}

func (s *Subscription) accepts(event Event) bool {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.matches(event)
}

func (s *Subscription) Events() <-chan Event    { return s.queue }
func (s *Subscription) Lagged() <-chan struct{} { return s.lagged }
func (s *Subscription) Done() <-chan struct{}   { return s.done }

func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		delete(s.hub.subs, s)
		s.hub.mu.Unlock()
		close(s.done)
	})
}

func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
