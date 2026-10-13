package events

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBus(t *testing.T) {
	var b Bus

	b.Publish(Event{Type: TypeStreamOnline}) // no subscribers: nothing happens

	s1 := b.Subscribe(10)
	s2 := b.Subscribe(10)

	b.Publish(Event{Type: TypeStreamOnline, Path: "s/abc"})

	for _, s := range []*Subscription{s1, s2} {
		e := <-s.C
		require.Equal(t, TypeStreamOnline, e.Type)
		require.Equal(t, "s/abc", e.Path)
		require.False(t, e.Time.IsZero())
	}

	s1.Close()
	s1.Close() // closing twice is fine
	b.Publish(Event{Type: TypeStreamOffline})
	require.Equal(t, TypeStreamOffline, (<-s2.C).Type)

	_, ok := <-s1.C
	require.False(t, ok)
	require.Len(t, b.subs, 1)
}

func TestBusOverflow(t *testing.T) {
	var b Bus
	slow := b.Subscribe(2)
	fast := b.Subscribe(100)

	for range 5 {
		b.Publish(Event{Type: TypeReaderJoined})
	}

	// the slow subscriber got what fit, then its channel was closed.
	n := 0
	for range slow.C {
		n++
	}
	require.Equal(t, 2, n)
	require.Len(t, fast.C, 5)
	require.Len(t, b.subs, 1)

	slow.Close() // closing after an overflow is fine
}
