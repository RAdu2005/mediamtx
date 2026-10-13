package webrtc

import (
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/bluenviron/mediamtx/internal/test"
)

// newBenchTrack returns a video track set up on a peer connection that is
// not connected, so writes stop at pion's track and the benchmark measures
// MediaMTX's own per-packet work.
func newBenchTrack(b *testing.B) *OutboundTrack {
	tr := &OutboundTrack{Caps: webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
	}}
	pc := &PeerConnection{LocalRandomUDP: true, Publish: true, OutboundTracks: []*OutboundTrack{tr}, Log: test.NilLogger}
	if err := pc.Start(); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(pc.Close)
	return tr
}

// BenchmarkOutboundTrackWrite writes packets: the path every packet of every
// reader takes.
func BenchmarkOutboundTrackWrite(b *testing.B) {
	tr := newBenchTrack(b)
	pkt := &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 96}, Payload: make([]byte, 1100)}
	ntp := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pkt.SequenceNumber = uint16(i)
		tr.WriteRTPWithNTP(pkt, ntp) //nolint:errcheck
	}
}

// BenchmarkOutboundTrackUnit checks a unit and writes its packets, like
// FromStream does for a 1080p frame of about 10 packets.
func BenchmarkOutboundTrackUnit(b *testing.B) {
	tr := newBenchTrack(b)
	tr.skipUnit(true) // the first key frame
	pkt := &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 96}, Payload: make([]byte, 1100)}
	ntp := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if tr.skipUnit(i%60 == 0) {
			continue
		}
		for j := 0; j < 10; j++ {
			pkt.SequenceNumber = uint16(i*10 + j)
			tr.WriteRTPWithNTP(pkt, ntp) //nolint:errcheck
		}
	}
}
