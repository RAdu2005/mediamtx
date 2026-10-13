package webrtc

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/pion/rtp"
	pwebrtc "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/webrtc"
	"github.com/bluenviron/mediamtx/internal/protocols/whip"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/bluenviron/mediamtx/internal/unit"
)

// readHarness is a WebRTC server with one H264 stream, a source that sends
// a key frame every 10 frames, and a WHEP reader.
type readHarness struct {
	s         *Server
	packets   chan *rtp.Packet
	keyFrames atomic.Int64 // requests that reached the source
}

// newReadHarness starts the server and, unless query is nil, a WHEP reader
// with that query.
func newReadHarness(t *testing.T, query *string, configure func(*Server)) *readHarness {
	h := &readHarness{packets: make(chan *rtp.Packet, 4096)}

	desc := &description.Session{Medias: []*description.Media{test.UniqueMediaH264()}}
	strm := &stream.Stream{
		OrigDesc:          desc,
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		Parent:            test.NilLogger,
	}
	require.NoError(t, strm.Initialize())
	t.Cleanup(strm.Close)

	subStream := &stream.SubStream{Stream: strm}
	require.NoError(t, subStream.Initialize())

	strm.SetKeyFrameRequester(func() { h.keyFrames.Add(1) })

	h.s = &Server{
		Address:               "127.0.0.1:8886",
		ReadTimeout:           conf.Duration(10 * time.Second),
		WriteTimeout:          conf.Duration(10 * time.Second),
		LocalUDPAddress:       "127.0.0.1:8887",
		LocalTCPAddress:       "127.0.0.1:8887",
		IPsFromInterfaces:     true,
		IPsFromInterfacesList: []string{},
		AdditionalHosts:       []string{},
		ICEServers:            []conf.WebRTCICEServer{},
		STUNGatherTimeout:     conf.Duration(5 * time.Second),
		HandshakeTimeout:      conf.Duration(10 * time.Second),
		TrackGatherTimeout:    conf.Duration(2 * time.Second),
		PathManager: &test.PathManager{
			FindPathConfImpl: func(_ defs.PathFindPathConfReq) (*defs.PathFindPathConfRes, error) {
				return &defs.PathFindPathConfRes{Conf: &conf.Path{}}, nil
			},
			AddReaderImpl: func(_ defs.PathAddReaderReq) (*defs.PathAddReaderRes, error) {
				return &defs.PathAddReaderRes{Path: &dummyPath{}, Stream: strm}, nil
			},
		},
		Parent: test.NilLogger,
	}
	if configure != nil {
		configure(h.s)
	}
	require.NoError(t, h.s.Initialize())
	t.Cleanup(h.s.Close)

	writerTerminate := make(chan struct{})
	writerDone := make(chan struct{})
	t.Cleanup(func() {
		close(writerTerminate)
		<-writerDone
	})

	go func() {
		defer close(writerDone)
		strm.WaitForReaders()

		for i := 0; ; i++ {
			nalu := []byte{1, byte(i)}
			if i%10 == 0 {
				nalu = []byte{5, byte(i)}
			}
			subStream.WriteUnit(desc.Medias[0], desc.Medias[0].Formats[0], &unit.Unit{
				PTS:     int64(i) * 450,
				NTP:     time.Now(),
				Payload: unit.PayloadH264{nalu},
			})

			select {
			case <-time.After(5 * time.Millisecond):
			case <-writerTerminate:
				return
			}
		}
	}()

	if query == nil {
		return h
	}

	u, err := url.Parse("http://localhost:8886/teststream/whep" + *query)
	require.NoError(t, err)

	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)

	wc := &whip.Client{HTTPClient: &http.Client{Transport: tr}, URL: u, Log: test.NilLogger}
	require.NoError(t, wc.Initialize(context.Background()))
	t.Cleanup(func() { wc.Close() }) //nolint:errcheck

	wc.InboundTracks()[0].OnPacketRTP = func(pkt *rtp.Packet) { h.packets <- pkt }
	wc.StartReading()

	return h
}

func (h *readHarness) drain(d time.Duration) []*rtp.Packet {
	var ret []*rtp.Packet
	timeout := time.After(d)
	for {
		select {
		case pkt := <-h.packets:
			ret = append(ret, pkt)
		case <-timeout:
			return ret
		}
	}
}

func (h *readHarness) session(t *testing.T) defs.APIWebRTCSession {
	list, err := h.s.APISessionsList()
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	return list.Items[0]
}

func isSTAPA(pkt *rtp.Packet) bool {
	return pkt.Payload[0]&0x1f == 24
}

func TestServerPauseAPI(t *testing.T) {
	h := newReadHarness(t, ptr(""), nil)
	require.NotEmpty(t, h.drain(200*time.Millisecond))

	sx := h.session(t)
	require.False(t, sx.Paused)
	require.Nil(t, sx.PausedSince)

	require.NoError(t, h.s.APISessionsPause(sx.ID, true, true))
	h.drain(100 * time.Millisecond) // packets in flight
	require.Empty(t, h.drain(300*time.Millisecond))

	sx = h.session(t)
	require.True(t, sx.Paused)
	require.NotNil(t, sx.PausedSince)
	require.True(t, sx.VideoPaused)
	require.True(t, sx.AudioPaused)

	requests := h.keyFrames.Load()
	require.NoError(t, h.s.APISessionsResume(sx.ID, true, true))

	after := h.drain(300 * time.Millisecond)
	require.NotEmpty(t, after)
	require.True(t, isSTAPA(after[0]), "the first frame after a resume is a key frame")
	require.Greater(t, h.keyFrames.Load(), requests, "resuming asks the source for a key frame")

	sx = h.session(t)
	require.False(t, sx.Paused)
	require.Nil(t, sx.PausedSince)

	require.ErrorIs(t, h.s.APISessionsPause([16]byte{}, true, true), ErrSessionNotFound)
}

func TestServerPauseVideoOnly(t *testing.T) {
	h := newReadHarness(t, ptr(""), nil)
	h.drain(200 * time.Millisecond)
	sx := h.session(t)

	require.NoError(t, h.s.APISessionsPause(sx.ID, true, false))
	h.drain(100 * time.Millisecond)
	require.Empty(t, h.drain(300*time.Millisecond))

	sx = h.session(t)
	require.True(t, sx.VideoPaused)
	require.False(t, sx.AudioPaused)
	require.True(t, sx.Paused, "a stream without audio is fully paused when its video is")
}

func ptr[T any](v T) *T { return &v }

// Browsers learn tracks from the SDP, so a session can start without
// sending anything. A pion receiver stands in for the browser.
func TestServerStartPausedWithoutPreview(t *testing.T) {
	h := newReadHarness(t, nil, nil)

	pc, err := pwebrtc.NewPeerConnection(pwebrtc.Configuration{})
	require.NoError(t, err)
	defer pc.Close() //nolint:errcheck

	for _, kind := range []pwebrtc.RTPCodecType{pwebrtc.RTPCodecTypeVideo, pwebrtc.RTPCodecTypeAudio} {
		_, err = pc.AddTransceiverFromKind(kind, pwebrtc.RTPTransceiverInit{Direction: pwebrtc.RTPTransceiverDirectionRecvonly})
		require.NoError(t, err)
	}

	firstPacket := make(chan *rtp.Packet, 1)
	pc.OnTrack(func(track *pwebrtc.TrackRemote, _ *pwebrtc.RTPReceiver) {
		if track.Kind() != pwebrtc.RTPCodecTypeVideo {
			return
		}
		pkt, _, err2 := track.ReadRTP()
		if err2 == nil {
			firstPacket <- pkt
		}
	})

	offer, err := pc.CreateOffer(nil)
	require.NoError(t, err)
	gathered := pwebrtc.GatheringCompletePromise(pc)
	require.NoError(t, pc.SetLocalDescription(offer))
	<-gathered

	res, err := http.Post("http://localhost:8886/teststream/whep?paused=true&preview=false",
		"application/sdp", strings.NewReader(pc.LocalDescription().SDP))
	require.NoError(t, err)
	answer, err := io.ReadAll(res.Body)
	res.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.StatusCode, string(answer))
	require.NoError(t, pc.SetRemoteDescription(pwebrtc.SessionDescription{Type: pwebrtc.SDPTypeAnswer, SDP: string(answer)}))

	require.Eventually(t, func() bool { return h.session(t).PeerConnectionEstablished }, 10*time.Second, 50*time.Millisecond)

	select {
	case <-firstPacket:
		t.Fatal("a session started without preview sent a packet")
	case <-time.After(500 * time.Millisecond):
	}

	sx := h.session(t)
	require.True(t, sx.Paused)
	require.NotNil(t, sx.PausedSince)

	require.NoError(t, h.s.APISessionsResume(sx.ID, true, true))

	select {
	case pkt := <-firstPacket:
		require.True(t, isSTAPA(pkt), "the first frame after a resume is a key frame")
	case <-time.After(3 * time.Second):
		t.Fatal("nothing received after resuming")
	}
}

func TestServerPausedTimeout(t *testing.T) {
	h := newReadHarness(t, ptr(""), func(s *Server) {
		s.PausedTimeout = conf.Duration(1500 * time.Millisecond)
	})
	h.drain(200 * time.Millisecond)
	sx := h.session(t)

	// sessions that play are never closed.
	time.Sleep(2 * time.Second)
	h.session(t)

	require.NoError(t, h.s.APISessionsPause(sx.ID, true, true))

	require.Eventually(t, func() bool {
		list, err := h.s.APISessionsList()
		require.NoError(t, err)
		return len(list.Items) == 0
	}, 5*time.Second, 100*time.Millisecond, "a forgotten paused session is closed")
}

func TestServerNewReaderStartsWithKeyFrame(t *testing.T) {
	h := newReadHarness(t, ptr(""), nil)
	first := h.drain(300 * time.Millisecond)
	require.NotEmpty(t, first)
	require.True(t, isSTAPA(first[0]), "a new reader's first frame is a key frame")
}

type testLogger struct{}

func (testLogger) Log(logger.Level, string, ...any) {}

// A pause request that arrives before the peer connection exists is stored
// and applied once it does, instead of blocking.
func TestSessionPauseBeforeConnection(t *testing.T) {
	sx := &session{parent: &Server{Parent: testLogger{}}}

	done := make(chan error)
	go func() { done <- sx.setPaused(PauseTracks{Video: true}, true) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("setPaused blocked")
	}

	strm := &stream.Stream{
		OrigDesc:          &description.Session{Medias: []*description.Media{test.UniqueMediaH264()}},
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		Parent:            test.NilLogger,
	}
	require.NoError(t, strm.Initialize())
	defer strm.Close()

	pc := &webrtc.PeerConnection{Publish: true, Log: test.NilLogger}
	require.NoError(t, webrtc.FromStream(strm.OrigDesc, &stream.Reader{Parent: test.NilLogger}, pc))

	sx.attachPausable(pc)
	video, _ := pc.PausedTracks()
	require.True(t, video)

	paused, since, videoPaused, audioPaused := sx.pauseState()
	require.True(t, paused)
	require.NotNil(t, since)
	require.True(t, videoPaused)
	require.False(t, audioPaused)

	require.ErrorIs(t, (&session{publish: true}).setPaused(AllTracks, true), ErrSessionNotReading)
}

// Readers that join, resume or fall behind ask the publisher for a key
// frame: the request travels through the stream to the publishing session
// and reaches the publisher as a PLI.
func TestServerPublisherReceivesKeyFrameRequest(t *testing.T) {
	var subStream *stream.SubStream
	published := make(chan struct{})

	s := &Server{
		Address:               "127.0.0.1:8886",
		TrustedProxies:        conf.IPNetworks{},
		ReadTimeout:           conf.Duration(10 * time.Second),
		WriteTimeout:          conf.Duration(10 * time.Second),
		LocalUDPAddress:       "127.0.0.1:8887",
		LocalTCPAddress:       "127.0.0.1:8887",
		IPsFromInterfaces:     true,
		IPsFromInterfacesList: []string{},
		AdditionalHosts:       []string{},
		ICEServers:            []conf.WebRTCICEServer{},
		STUNGatherTimeout:     conf.Duration(5 * time.Second),
		HandshakeTimeout:      conf.Duration(10 * time.Second),
		TrackGatherTimeout:    conf.Duration(2 * time.Second),
		// periodic requests off, so that only the reader's request counts.
		KeyFrameRequestPeriod: 0,
		PathManager: &test.PathManager{
			FindPathConfImpl: func(_ defs.PathFindPathConfReq) (*defs.PathFindPathConfRes, error) {
				return &defs.PathFindPathConfRes{Conf: &conf.Path{}}, nil
			},
			AddPublisherImpl: func(req defs.PathAddPublisherReq) (*defs.PathAddPublisherRes, error) {
				strm := &stream.Stream{
					OrigDesc:          req.Desc,
					WriteQueueSize:    512,
					RTPMaxPayloadSize: 1450,
					Parent:            test.NilLogger,
				}
				require.NoError(t, strm.Initialize())
				subStream = &stream.SubStream{Stream: strm, UseRTPPackets: true}
				require.NoError(t, subStream.Initialize())
				close(published)
				return &defs.PathAddPublisherRes{Path: &dummyPath{}, SubStream: subStream}, nil
			},
		},
		Parent: test.NilLogger,
	}
	require.NoError(t, s.Initialize())
	defer s.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()

	u, err := url.Parse("http://localhost:8886/teststream/whip")
	require.NoError(t, err)

	var requests atomic.Int64
	track := &webrtc.OutboundTrack{
		Caps: pwebrtc.RTPCodecCapability{
			MimeType:    pwebrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
		},
		RequestKeyFrame: func(bool) { requests.Add(1) },
	}

	wc := &whip.Client{
		HTTPClient:     &http.Client{Transport: tr},
		URL:            u,
		Publish:        true,
		OutboundTracks: []*webrtc.OutboundTrack{track},
		Log:            test.NilLogger,
	}
	require.NoError(t, wc.Initialize(context.Background()))
	defer checkClose(t, wc.Close)

	require.NoError(t, track.WriteRTP(&rtp.Packet{
		Header:  rtp.Header{Version: 2, Marker: true, PayloadType: 96, SequenceNumber: 1, Timestamp: 1},
		Payload: []byte{5},
	}))
	<-published
	defer subStream.Stream.Close()

	time.Sleep(2500 * time.Millisecond)
	require.Equal(t, int64(0), requests.Load(), "periodic requests are disabled")

	require.Eventually(t, func() bool {
		return subStream.Stream.RequestKeyFrame()
	}, 5*time.Second, 50*time.Millisecond)

	require.Eventually(t, func() bool { return requests.Load() >= 1 }, 5*time.Second, 20*time.Millisecond,
		"the publisher received a PLI")
}
