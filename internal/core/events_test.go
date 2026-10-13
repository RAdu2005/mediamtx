package core

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/test"
)

// The event stream reports a stream becoming available and online, and
// unavailable and offline again, without polling.
func TestEventsStream(t *testing.T) {
	p, ok := newInstance(t, "api: yes\n"+
		"paths:\n"+
		"  all_others:\n")
	require.True(t, ok)
	defer p.Close()

	res, err := http.Get("http://localhost:9997/v3/events")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	r := bufio.NewReader(res.Body)
	next := func() (string, events.Event) {
		var name string
		var ev events.Event
		for {
			line, err2 := r.ReadString('\n')
			require.NoError(t, err2)
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev))
			case line == "" && name != "":
				return name, ev
			}
		}
	}
	waitFor := func(want string) events.Event {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			name, ev := next()
			if name == want {
				return ev
			}
		}
		t.Fatalf("no %s event", want)
		return events.Event{}
	}

	name, _ := next()
	require.Equal(t, "ready", name)

	c := gortsplib.Client{}
	err = c.StartRecording("rtsp://localhost:8554/mystream?u=alice",
		&description.Session{Medias: []*description.Media{test.UniqueMediaH264()}})
	require.NoError(t, err)

	ev := waitFor("stream.available")
	require.Equal(t, "mystream", ev.Path)
	require.Equal(t, "u=alice", ev.Query)
	require.Equal(t, "rtspSession", string(ev.Source.Type))

	ev = waitFor("stream.online")
	require.Equal(t, "mystream", ev.Path)

	c.Close()

	ev = waitFor("stream.offline")
	require.Equal(t, "mystream", ev.Path)
	ev = waitFor("stream.unavailable")
	require.Equal(t, "mystream", ev.Path)
}
