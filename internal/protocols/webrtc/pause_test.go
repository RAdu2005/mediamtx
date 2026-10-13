package webrtc_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/protocols/webrtc"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/bluenviron/mediamtx/internal/unit"
)

// pauseHarness connects a reader to a MediaMTX stream through FromStream,
// like a WHEP session does.
type pauseHarness struct {
	t          *testing.T
	strm       *stream.Stream
	subStream  *stream.SubStream
	publisher  *webrtc.PeerConnection
	reader     *stream.Reader
	packets    chan *rtp.Packet
	keyFrames  atomic.Int64 // key frame requests from the outbound tracks
	retries    atomic.Int64
	pts        int64
	rtpPackets bool
	format     format.Format
}

func newPauseHarness(t *testing.T, forma format.Format, rtpPackets bool) *pauseHarness {
	h := &pauseHarness{t: t, packets: make(chan *rtp.Packet, 4096), rtpPackets: rtpPackets, format: forma}

	mediaType := description.MediaTypeVideo
	if _, ok := forma.(*format.Opus); ok {
		mediaType = description.MediaTypeAudio
	}

	h.strm = &stream.Stream{
		OrigDesc: &description.Session{Medias: []*description.Media{{
			Type:    mediaType,
			Formats: []format.Format{forma},
		}}},
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		Parent:            test.NilLogger,
	}
	require.NoError(t, h.strm.Initialize())
	t.Cleanup(h.strm.Close)

	h.subStream = &stream.SubStream{Stream: h.strm, UseRTPPackets: rtpPackets}
	require.NoError(t, h.subStream.Initialize())

	pcReader := &webrtc.PeerConnection{
		LocalRandomUDP:    true,
		IPsFromInterfaces: true,
		Publish:           false,
		Log:               test.NilLogger,
	}
	require.NoError(t, pcReader.Start())
	t.Cleanup(pcReader.Close)

	h.publisher = &webrtc.PeerConnection{
		LocalRandomUDP:    true,
		IPsFromInterfaces: true,
		Publish:           true,
		Log:               test.NilLogger,
	}

	h.reader = &stream.Reader{Parent: test.NilLogger}
	require.NoError(t, webrtc.FromStream(h.strm.OrigDesc, h.reader, h.publisher))

	h.publisher.SetKeyFrameRequester(func(retry bool) {
		h.keyFrames.Add(1)
		if retry {
			h.retries.Add(1)
		}
	})

	require.NoError(t, h.publisher.Start())
	t.Cleanup(h.publisher.Close)

	offer, err := pcReader.CreatePartialOffer(false)
	require.NoError(t, err)
	answer, err := h.publisher.CreateFullAnswer(offer, false)
	require.NoError(t, err)
	require.NoError(t, pcReader.SetAnswer(answer))
	require.NoError(t, pcReader.WaitUntilConnected(10*time.Second))
	require.NoError(t, h.publisher.WaitUntilConnected(10*time.Second))

	h.strm.AddReader(h.reader)
	t.Cleanup(func() { h.strm.RemoveReader(h.reader) })

	// the first unit must be a key frame for a new reader to receive it,
	// and the reader needs a packet to discover the track.
	h.write(true)
	require.NoError(t, pcReader.GatherInboundTracks(2*time.Second))
	pcReader.InboundTracks()[0].OnPacketRTP = func(pkt *rtp.Packet) { h.packets <- pkt }
	pcReader.StartReading()

	return h
}

// write writes a key frame or another frame (audio has no difference).
func (h *pauseHarness) write(keyFrame bool) {
	h.pts += 3000
	u := &unit.Unit{PTS: h.pts, NTP: time.Now()}

	switch h.format.(type) {
	case *format.H264:
		if keyFrame {
			u.Payload = unit.PayloadH264{{5, 1}}
		} else {
			u.Payload = unit.PayloadH264{{1, 1}}
		}
	case *format.VP8:
		if keyFrame {
			u.Payload = unit.PayloadVP8{0, 2}
		} else {
			u.Payload = unit.PayloadVP8{1, 2}
		}
	case *format.VP9:
		if keyFrame {
			u.Payload = unit.PayloadVP9{0x82, 0x49, 0x83, 0x42, 0x0, 0x77, 0xf0, 0x32, 0x34}
		} else {
			u.Payload = unit.PayloadVP9{0x86, 0x00, 0x00}
		}
	case *format.AV1:
		if keyFrame {
			u.Payload = unit.PayloadAV1{{0x08, 2}} // sequence header OBU
		} else {
			u.Payload = unit.PayloadAV1{{0x30, 2}} // frame OBU
		}
	case *format.Opus:
		u.RTPPackets = []*rtp.Packet{{
			Header:  rtp.Header{Version: 2, Marker: true, PayloadType: 111, SequenceNumber: uint16(h.pts / 3000), Timestamp: uint32(h.pts)},
			Payload: []byte{1},
		}}
	}

	media := h.strm.OrigDesc.Medias[0]
	h.subStream.WriteUnit(media, media.Formats[0], u)
}

func (h *pauseHarness) drain(d time.Duration) []*rtp.Packet {
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

func TestPauseResumeCodecs(t *testing.T) {
	for _, ca := range []struct {
		name       string
		format     format.Format
		rtpPackets bool
		video      bool
	}{
		{"h264", test.FormatH264, false, true},
		{"vp8", &format.VP8{PayloadTyp: 96}, false, true},
		{"vp9", &format.VP9{PayloadTyp: 96}, false, true},
		{"av1", &format.AV1{PayloadTyp: 96}, false, true},
		{"opus", &format.Opus{PayloadTyp: 111, ChannelCount: 2}, true, false},
	} {
		t.Run(ca.name, func(t *testing.T) {
			h := newPauseHarness(t, ca.format, ca.rtpPackets)

			before := h.drain(200 * time.Millisecond)
			require.NotEmpty(t, before)
			lastSeq := before[len(before)-1].SequenceNumber

			h.publisher.SetPaused(true, true, true)
			video, audio := h.publisher.PausedTracks()
			require.True(t, video && audio)

			for range 5 {
				h.write(true)
			}
			require.Empty(t, h.drain(200*time.Millisecond), "nothing is sent while paused")

			requestsBefore := h.keyFrames.Load()
			h.publisher.SetPaused(true, true, false)

			if ca.video {
				// a key frame is requested straight away, and frames that
				// reference frames sent before the pause are not sent.
				require.Equal(t, requestsBefore+1, h.keyFrames.Load())
				h.write(false)
				h.write(false)
				require.Empty(t, h.drain(200*time.Millisecond), "video waits for a key frame after a resume")
			}

			h.write(true)
			after := h.drain(300 * time.Millisecond)
			require.NotEmpty(t, after)
			require.Equal(t, lastSeq+1, after[0].SequenceNumber, "sequence numbers stay contiguous")
		})
	}
}

func TestPauseOnlyVideo(t *testing.T) {
	h := newPauseHarness(t, test.FormatH264, false)
	h.drain(200 * time.Millisecond)

	h.publisher.SetPaused(true, false, true)
	video, audio := h.publisher.PausedTracks()
	require.True(t, video)
	require.True(t, audio, "a stream without audio counts as audio paused")

	h.write(true)
	require.Empty(t, h.drain(200*time.Millisecond))

	// pausing audio has no effect on video.
	h.publisher.SetPaused(false, true, false)
	h.write(true)
	require.Empty(t, h.drain(200*time.Millisecond))

	h.publisher.SetPaused(true, false, false)
	h.write(true)
	require.NotEmpty(t, h.drain(200*time.Millisecond))
}

func TestNewReaderWaitsForKeyFrame(t *testing.T) {
	h := newPauseHarness(t, &format.VP8{PayloadTyp: 96}, false)
	require.NotEmpty(t, h.drain(200*time.Millisecond))

	// the harness's first frame was a key frame and needed no request.
	require.Equal(t, int64(0), h.keyFrames.Load())
}

func TestKeyFrameRetry(t *testing.T) {
	h := newPauseHarness(t, test.FormatH264, false)
	h.drain(200 * time.Millisecond)

	h.publisher.SetPaused(true, true, true)
	h.publisher.SetPaused(true, true, false)
	require.Equal(t, int64(1), h.keyFrames.Load())

	// the source keeps sending frames that are not key frames.
	deadline := time.Now().Add(2700 * time.Millisecond)
	for time.Now().Before(deadline) {
		h.write(false)
		time.Sleep(50 * time.Millisecond)
	}
	require.Empty(t, h.drain(100*time.Millisecond))
	// asked again after 500 ms, which is not reported as a retry, then after
	// 2 more seconds (the reader's own periodic PLIs are forwarded too).
	require.GreaterOrEqual(t, h.keyFrames.Load(), int64(3))
	require.Equal(t, int64(1), h.retries.Load(), "the third request is reported as a retry")
}

func TestRequireKeyFrameAfterDiscard(t *testing.T) {
	h := newPauseHarness(t, test.FormatH264, false)
	h.drain(200 * time.Millisecond)

	h.publisher.RequireKeyFrame()
	require.Equal(t, int64(1), h.keyFrames.Load())

	h.write(false)
	require.Empty(t, h.drain(200*time.Millisecond))

	h.write(true)
	require.NotEmpty(t, h.drain(200*time.Millisecond))
}

func TestStartPausedWithoutPreview(t *testing.T) {
	strm := &stream.Stream{
		OrigDesc:          &description.Session{Medias: []*description.Media{test.UniqueMediaH264()}},
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		Parent:            test.NilLogger,
	}
	require.NoError(t, strm.Initialize())
	defer strm.Close()

	pc := &webrtc.PeerConnection{Publish: true, Log: test.NilLogger}
	r := &stream.Reader{Parent: test.NilLogger}
	require.NoError(t, webrtc.FromStream(strm.OrigDesc, r, pc))

	pc.StartPaused(false)
	video, _ := pc.PausedTracks()
	require.True(t, video)

	pc.StartPaused(true)
	video, _ = pc.PausedTracks()
	require.False(t, video, "with a preview the track pauses only after its first key frame")
}

// Pausing and resuming from other goroutines while units flow must be
// race-free (run with -race).
func TestPauseToggleRace(t *testing.T) {
	h := newPauseHarness(t, test.FormatH264, false)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			h.write(i%10 == 0)
			time.Sleep(time.Millisecond)
		}
	}()

	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 200 {
				switch (i + j) % 4 {
				case 0:
					h.publisher.SetPaused(true, true, true)
				case 1:
					h.publisher.SetPaused(true, false, false)
				case 2:
					h.publisher.RequireKeyFrame()
				case 3:
					h.publisher.PausedTracks()
				}
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	// after all that, a resume and a key frame bring the reader back.
	h.publisher.SetPaused(true, true, false)
	h.drain(100 * time.Millisecond)
	h.write(true)
	require.NotEmpty(t, h.drain(300*time.Millisecond))
}

// The reader's receiver reports and feedback reach the outbound statistics.
func TestOutboundStatsFromReader(t *testing.T) {
	h := newPauseHarness(t, test.FormatH264, false)

	// the reader (a MediaMTX peer connection) sends receiver reports every
	// second and a key frame request every 2 seconds.
	deadline := time.Now().Add(3500 * time.Millisecond)
	for i := 0; time.Now().Before(deadline); i++ {
		h.write(i%30 == 0)
		time.Sleep(33 * time.Millisecond)
	}

	stats := h.publisher.Stats()
	require.Positive(t, stats.OutboundRoundTripTime)
	require.Less(t, stats.OutboundRoundTripTime, 1.0)
	require.Equal(t, uint64(0), stats.OutboundRTPPacketsLost)
	require.Positive(t, stats.OutboundPLIs)
}
