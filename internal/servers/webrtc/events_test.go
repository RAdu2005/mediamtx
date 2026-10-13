package webrtc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/events"
)

func waitEvent(t *testing.T, sub *events.Subscription, typ events.Type) events.Event {
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e := <-sub.C:
			if e.Type == typ {
				return e
			}
		case <-timeout:
			t.Fatalf("no %s event", typ)
		}
	}
}

func TestServerEvents(t *testing.T) {
	sub := events.Default.Subscribe(1024)
	defer sub.Close()

	h := newReadHarness(t, ptr("?u=viewer1"), nil)
	id := h.session(t).ID.String()

	created := waitEvent(t, sub, events.TypeSessionCreated)
	require.Equal(t, id, created.Reader.ID)
	require.Equal(t, "teststream", created.Path)
	require.Equal(t, "u=viewer1", created.Query)
	require.Equal(t, "read", created.State)

	joined := waitEvent(t, sub, events.TypeReaderJoined)
	require.Equal(t, id, joined.Reader.ID)
	require.Equal(t, "u=viewer1", joined.Query)

	require.NoError(t, h.s.APISessionsKick(h.session(t).ID))

	left := waitEvent(t, sub, events.TypeReaderLeft)
	require.Equal(t, id, left.Reader.ID)
	closed := waitEvent(t, sub, events.TypeSessionClosed)
	require.Equal(t, id, closed.Reader.ID)
}
