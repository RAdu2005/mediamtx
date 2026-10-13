// Package events broadcasts what happens on the server (streams becoming
// available, readers joining, sessions opening) to subscribers of the API's
// event stream. Publishing never blocks: a subscriber that does not keep up
// is dropped and has to reconnect and re-synchronize with the list endpoints.
package events

import (
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// Type is the type of an event.
type Type string

// event types.
const (
	TypeStreamAvailable   Type = "stream.available"
	TypeStreamUnavailable Type = "stream.unavailable"
	TypeStreamOnline      Type = "stream.online"
	TypeStreamOffline     Type = "stream.offline"
	TypeReaderJoined      Type = "reader.joined"
	TypeReaderLeft        Type = "reader.left"
	TypeConnOpened        Type = "conn.opened"
	TypeConnClosed        Type = "conn.closed"
	TypeSessionCreated    Type = "session.created"
	TypeSessionClosed     Type = "session.closed"
)

// Event is something that happened on the server.
type Event struct {
	Type Type      `json:"type"`
	Time time.Time `json:"time"`

	// path of the stream, when the event concerns one.
	Path string `json:"path,omitempty"`
	// query of the request that published, read or opened the session.
	Query string `json:"query,omitempty"`
	// publisher of a stream event.
	Source *defs.APIPathSource `json:"source,omitempty"`
	// reader of a reader event, or connection or session of a conn or session event.
	Reader *defs.APIPathReader `json:"reader,omitempty"`

	// sessions and connections.
	User       string `json:"user,omitempty"`
	RemoteAddr string `json:"remoteAddr,omitempty"`
	State      string `json:"state,omitempty"` // "publish" or "read"
}

// Subscription receives events until it is closed or overflows.
type Subscription struct {
	// C receives events. It is closed when the subscriber did not keep up.
	C chan Event

	bus    *Bus
	closed bool
}

// Close stops the subscription.
func (s *Subscription) Close() {
	s.bus.mutex.Lock()
	defer s.bus.mutex.Unlock()

	if !s.closed {
		s.closed = true
		delete(s.bus.subs, s)
		close(s.C)
	}
}

// Bus delivers events to subscribers.
type Bus struct {
	mutex sync.Mutex
	subs  map[*Subscription]struct{}
}

// Subscribe returns a subscription with room for buffer pending events.
func (b *Bus) Subscribe(buffer int) *Subscription {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.subs == nil {
		b.subs = make(map[*Subscription]struct{})
	}

	s := &Subscription{C: make(chan Event, buffer), bus: b}
	b.subs[s] = struct{}{}
	return s
}

// Publish sends an event to every subscriber without blocking.
func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	for s := range b.subs {
		select {
		case s.C <- e:
		default:
			// the subscriber fell behind: drop it rather than slow down the
			// server, so that it reconnects and re-synchronizes.
			s.closed = true
			delete(b.subs, s)
			close(s.C)
		}
	}
}

// Default is the bus used by the hooks and the API.
var Default = &Bus{}

// Publish sends an event on the default bus.
func Publish(e Event) {
	Default.Publish(e)
}

// CountSubscribers returns the number of subscribers of b, for tests.
func CountSubscribers(b *Bus) int {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return len(b.subs)
}
