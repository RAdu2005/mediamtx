package api //nolint:revive

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/test"
)

type sseEvent struct {
	name string
	data string
}

func readSSE(t *testing.T, r *bufio.Reader) sseEvent {
	var e sseEvent
	for {
		line, err := r.ReadString('\n')
		require.NoError(t, err)
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "":
			if e.name != "" {
				return e
			}
		case strings.HasPrefix(line, "event: "):
			e.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			e.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func TestEvents(t *testing.T) {
	bus := &events.Bus{}
	prev := eventsBus
	eventsBus = bus
	defer func() { eventsBus = prev }()

	api := API{
		Address:      "localhost:9997",
		ReadTimeout:  conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second),
		AuthManager:  test.NilAuthManager,
		Parent:       &testParent{},
	}
	err := api.Initialize()
	require.NoError(t, err)
	defer api.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	connect := func() (*http.Response, *bufio.Reader) {
		res, err2 := hc.Get("http://localhost:9997/v3/events")
		require.NoError(t, err2)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
		r := bufio.NewReader(res.Body)
		require.Equal(t, "ready", readSSE(t, r).name, "subscribed before anything is sent")
		return res, r
	}

	for range 2 { // the second round is a reconnect
		res, r := connect()

		bus.Publish(events.Event{
			Type:   events.TypeReaderJoined,
			Path:   "s/abc",
			Query:  "u=123",
			User:   "viewer",
			Reader: &defs.APIPathReader{Type: defs.APIPathReaderTypeWebRTCSession, ID: "id1"},
		})

		e := readSSE(t, r)
		require.Equal(t, "reader.joined", e.name)

		var ev events.Event
		require.NoError(t, json.Unmarshal([]byte(e.data), &ev))
		require.Equal(t, "s/abc", ev.Path)
		require.Equal(t, "u=123", ev.Query)
		require.Equal(t, "viewer", ev.User)
		require.Equal(t, "id1", ev.Reader.ID)

		res.Body.Close()

		// the subscription ends with the request.
		require.Eventually(t, func() bool {
			return events.CountSubscribers(bus) == 0
		}, 2*time.Second, 20*time.Millisecond)
	}
}
