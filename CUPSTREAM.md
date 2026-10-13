# CupStream build of MediaMTX

This fork carries the MediaMTX build used by [CupStream](https://github.com/RAdu2005/cupstream).

- Branches are named `cupstream/<upstream release>`, e.g. `cupstream/v1.21.2`, and start from that upstream tag.
- The first commit on each branch is upstream PR [#6308](https://github.com/bluenviron/mediamtx/pull/6308)
  ("webrtc: allow pausing and resuming WHEP sessions"), cherry-picked unchanged.
- CupStream's own changes follow as separate commits (see section 3.2 of the CupStream project overview).
- Releases are tagged `<upstream release>-cupstream.<n>`, e.g. `v1.21.2-cupstream.1`, and CupStream pins that tag.

Upgrading to a new upstream release:

```sh
git fetch upstream --tags
git switch -c cupstream/vX.Y.Z vX.Y.Z
git cherry-pick <commits from the previous cupstream branch>
go generate ./... && go test -race ./internal/servers/webrtc/... ./internal/protocols/webrtc/...
git tag vX.Y.Z-cupstream.1 && git push origin cupstream/vX.Y.Z vX.Y.Z-cupstream.1
```

## What the CupStream build adds

Numbers refer to section 3.2 of the CupStream project overview.

| Change | Commit |
|---|---|
| WHEP pause and resume (upstream PR #6308, unchanged) | `webrtc: allow pausing and resuming WHEP sessions` |
| 10. No reordering of inbound audio (upstream PR #6329, unchanged) | `Avoid reordering inbound WebRTC audio` |
| 11. `udpWriteBufferSize` (upstream PR #6331; conflicts resolved for v1.21.2) | `Add WebRTC UDP write buffer setting` |
| 15. Hooks are killed 10 s after SIGINT | `externalcmd: kill commands that ignore SIGINT` |
| 12. H264 Main and High from publishers | `webrtc: accept H264 Main and High from publishers` |
| 1–9. Key frames for new, resumed and lagging readers; PLI/FIR forwarding; `webrtcKeyFrameRequestPeriod`; pause/resume API with per-track selection; `webrtcAllowPausePatch`; non-blocking pause; `webrtcPausedTimeout`; paused state in the API; `preview=false`; lock-free per-unit check; key frame retry | `webrtc: complete WHEP pausing and key frame handling` |
| 13. `GET /v3/events` (server-sent events) | `api: add an event stream` |
| 14. Readers' loss, jitter, RTT, NACKs and PLIs in the API and metrics | `webrtc: report readers' packet loss, jitter and round-trip time` |
| Measured key frame interval of each path; the key frame retry warning is logged once per session | `stream: measure the key frame interval` |

New settings, with their defaults: `udpWriteBufferSize: 0` (OS default), `webrtcKeyFrameRequestPeriod: 2s`, `webrtcAllowPausePatch: true`, `webrtcPausedTimeout: 2m`.

New API: `POST /v3/webrtc/sessions/pause/{id}`, `POST /v3/webrtc/sessions/resume/{id}` (optional body `{"video": bool, "audio": bool}`), `GET /v3/events`. Sessions gain `paused`, `pausedSince`, `videoPaused`, `audioPaused`, `outboundRTPPacketsLost`, `outboundRTPPacketsLossRatio`, `outboundRTPPacketsJitter` (s), `outboundRoundTripTime` (s), `outboundNACKs` and `outboundPLIs`. Paths gain `keyFrameInterval` (s): the longest of the last four intervals between key frames of the first video track, which is how long a new reader waits when the publisher ignores key frame requests (OBS and FFmpeg do).
