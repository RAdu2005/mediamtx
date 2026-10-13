package api //nolint:revive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/events"
)

const (
	eventsBufferSize = 1024
	eventsKeepalive  = 15 * time.Second
)

// eventsBus is the bus the event stream subscribes to; tests replace it.
var eventsBus = events.Default

// onEvents streams server events as server-sent events. The first event,
// "ready", tells the client that it is subscribed, so it can then list the
// current state without missing changes. If the client does not keep up, the
// stream ends with an "overflow" event and the client has to reconnect.
func (a *API) onEvents(ctx *gin.Context) {
	sub := eventsBus.Subscribe(eventsBufferSize)
	defer sub.Close()

	w := ctx.Writer
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	write := func(event string, data any) bool {
		byts, err := json.Marshal(data)
		if err != nil {
			return false
		}
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, byts)
		if err != nil {
			return false
		}
		w.Flush()
		return true
	}

	if !write("ready", struct{}{}) {
		return
	}

	keepalive := time.NewTicker(eventsKeepalive)
	defer keepalive.Stop()

	for {
		select {
		case e, ok := <-sub.C:
			if !ok {
				write("overflow", struct{}{})
				return
			}
			if !write(string(e.Type), e) {
				return
			}

		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			w.Flush()

		case <-ctx.Request.Context().Done():
			return

		case <-a.ctx.Done():
			return
		}
	}
}
