# WebRTC clients

|           | supported codecs              |
| --------- | ----------------------------- |
| **video** | AV1, VP9, VP8, H265, H264     |
| **audio** | Opus, G722, G711 (PCMA, PCMU) |
| **other** | KLV                           |

WebRTC is an API that makes use of a set of protocols and methods to connect two clients together and allow them to exchange live media or data streams. You can read a stream with WebRTC and a web browser by visiting:

```
http://localhost:8889/mystream
```

WHEP is a WebRTC extension that allows to read streams by using a URL, without passing through a web page. This allows to use WebRTC as a general purpose streaming protocol. If you are using a software that supports WHEP, you can read a stream from the server by using this URL:

```
http://localhost:8889/mystream/whep
```

Be aware that not all browsers can read tracks with any codec, check [Codec support in browsers](../2-features/25-webrtc-specific-features.md#codec-support-in-browsers).

Depending on the network it may be difficult to establish a connection between server and clients, read [Solving WebRTC connectivity issues](../2-features/25-webrtc-specific-features.md#solving-webrtc-connectivity-issues).

A WHEP session can be paused and resumed without closing the peer connection, by sending a `PATCH` request to the session URL (the one returned in the `Location` header) with `Content-Type: application/json`:

```sh
curl -X PATCH -H "Content-Type: application/json" -d '{"paused":true}' http://localhost:8889/mystream/whep/session-secret
curl -X PATCH -H "Content-Type: application/json" -d '{"paused":false}' http://localhost:8889/mystream/whep/session-secret
```

While paused, no media is sent to the client, while the connection is kept alive. When resumed, video tracks restart from the next key frame. Pausing only affects the session that is paused, other readers of the same stream are not affected.

A session can also be opened paused, by adding `paused=true` to the query of the initial `POST` request (`http://localhost:8889/mystream/whep?paused=true`). In this case, each track sends its first key frame and then pauses, allowing clients to display a preview and detect tracks. With `paused=true&preview=false`, nothing is sent until the session is resumed, which suits browsers, since they learn tracks from the SDP.

Sessions can also be paused and resumed through the API, by their ID (returned in the `ID` header of the `POST` response): `POST /v3/webrtc/sessions/pause/{id}` and `POST /v3/webrtc/sessions/resume/{id}`. An optional body selects the tracks, for instance `{"video": true, "audio": false}` to pause only video. Clients can be prevented from pausing and resuming their own session with `webrtcAllowPausePatch: false`, and sessions that stay paused are closed after `webrtcPausedTimeout`. The API reports `paused`, `pausedSince`, `videoPaused` and `audioPaused` for each session.

New readers start from a key frame, and so do readers that resume or fall behind; in each case the publisher is asked for a key frame straight away (WebRTC publishers only), at most once every 300 milliseconds per stream.

Some clients that can read with WebRTC and WHEP are [web browsers](07-web-browsers.md), [GStreamer](09-gstreamer.md) and [Unity](14-unity.md).
