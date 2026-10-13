//nolint:dupl
package api //nolint:revive

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/servers/webrtc"
)

func (a *API) onWebRTCSessionsList(ctx *gin.Context) {
	data, err := a.WebRTCServer.APISessionsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("itemsPerPage"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onWebRTCSessionsGet(ctx *gin.Context) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	data, err := a.WebRTCServer.APISessionsGet(uuid)
	if err != nil {
		if errors.Is(err, webrtc.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onWebRTCSessionsKick(ctx *gin.Context) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	err = a.WebRTCServer.APISessionsKick(uuid)
	if err != nil {
		if errors.Is(err, webrtc.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	a.writeOK(ctx)
}

// webRTCPauseTracks is the optional body of the pause and resume endpoints.
// Without a body, both video and audio are selected.
type webRTCPauseTracks struct {
	Video *bool `json:"video"`
	Audio *bool `json:"audio"`
}

func (a *API) onWebRTCSessionsPause(ctx *gin.Context) {
	a.onWebRTCSessionsSetPaused(ctx, a.WebRTCServer.APISessionsPause)
}

func (a *API) onWebRTCSessionsResume(ctx *gin.Context) {
	a.onWebRTCSessionsSetPaused(ctx, a.WebRTCServer.APISessionsResume)
}

func (a *API) onWebRTCSessionsSetPaused(ctx *gin.Context, apply func(uuid.UUID, bool, bool) error) {
	uuid, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	video, audio := true, true
	if ctx.Request.ContentLength != 0 {
		var body webRTCPauseTracks
		err = json.NewDecoder(&customLimitReader{ctx.Request.Body, 1024}).Decode(&body)
		if err != nil && !errors.Is(err, io.EOF) {
			a.writeError(ctx, http.StatusBadRequest, err)
			return
		}
		if body.Video != nil || body.Audio != nil {
			video = body.Video != nil && *body.Video
			audio = body.Audio != nil && *body.Audio
		}
	}

	err = apply(uuid, video, audio)
	if err != nil {
		switch {
		case errors.Is(err, webrtc.ErrSessionNotFound):
			a.writeError(ctx, http.StatusNotFound, err)
		case errors.Is(err, webrtc.ErrSessionNotReading):
			a.writeError(ctx, http.StatusBadRequest, err)
		default:
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	a.writeOK(ctx)
}
