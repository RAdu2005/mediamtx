# Control API

The server can be queried and controlled with an API, that can be enabled by toggling the `api` parameter in the configuration:

```yml
api: yes
```

To obtain a list of active paths, run:

```
curl http://127.0.0.1:9997/v3/paths/list
```

Changes can be followed without polling through the event stream, which uses [server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html):

```
curl -N http://127.0.0.1:9997/v3/events
```

The first event, `ready`, means that the subscription is active: list the current state after receiving it, then apply each event as it arrives. Events report streams becoming available, unavailable, online and offline, readers joining and leaving, and connections and WebRTC sessions opening and closing, each with the path, query, user and session or connection ID where they apply. A client that does not keep up receives `overflow` and the stream ends; it should reconnect and list the state again.

Each path reports `keyFrameInterval`, the longest of the recent intervals between key frames of its first video track, in seconds. It is how long a new reader may wait for its first frame when the publisher ignores key frame requests, as FFmpeg and OBS do; browsers answer them, so their interval stays near `webrtcKeyFrameRequestPeriod`.

The control API is documented in the [Control API Reference page](../5-references/2-control-api.md) and in the [OpenAPI / Swagger file](https://github.com/bluenviron/mediamtx/blob/{version_tag}/api/openapi.yaml).

Be aware that by default the Control API is accessible by localhost only; to increase visibility or enforce authentication, check [Authentication](06-authentication.md).
