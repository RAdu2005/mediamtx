package webrtc

import (
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

func TestOutboundTrackFeedback(t *testing.T) {
	requests := 0
	tr := &OutboundTrack{
		Caps:            webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000},
		RequestKeyFrame: func(bool) { requests++ },
		ssrc:            1234,
	}

	sent := time.Unix(1_800_000_000, 0)
	tr.recordSenderReport(&rtcp.SenderReport{NTPTime: 0x1122334455667788}, sent)

	tr.processRTCP([]rtcp.Packet{
		&rtcp.ReceiverReport{Reports: []rtcp.ReceptionReport{
			{SSRC: 999, TotalLost: 1000}, // another track
			{
				SSRC:             1234,
				FractionLost:     64,
				TotalLost:        7,
				Jitter:           9000,
				LastSenderReport: 0x33445566,
				Delay:            65536 / 2, // 0.5 s
			},
		}},
		&rtcp.TransportLayerNack{Nacks: []rtcp.NackPair{{PacketID: 10, LostPackets: 0b11}}},
		&rtcp.PictureLossIndication{},
		&rtcp.FullIntraRequest{},
	}, sent.Add(600*time.Millisecond))

	f := tr.Feedback()
	require.Equal(t, uint64(7), f.PacketsLost)
	require.Equal(t, 0.25, f.LossRatio)
	require.InDelta(t, 0.1, f.Jitter, 1e-9)
	require.Equal(t, 100*time.Millisecond, f.RoundTripTime)
	require.Equal(t, uint64(3), f.NACKs, "packet 10 and the two in the bitmask")
	require.Equal(t, uint64(2), f.PLIs)
	require.Equal(t, 2, requests, "PLI and FIR are passed on to the source")

	// a report that refers to an unknown sender report leaves the RTT alone.
	tr.processRTCP([]rtcp.Packet{&rtcp.ReceiverReport{Reports: []rtcp.ReceptionReport{
		{SSRC: 1234, LastSenderReport: 0xdeadbeef},
	}}}, sent.Add(time.Hour))
	require.Equal(t, 100*time.Millisecond, tr.Feedback().RoundTripTime)
}
