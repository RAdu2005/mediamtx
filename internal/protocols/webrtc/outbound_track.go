package webrtc

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/rtpsender"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// keyFrameQuickRetryPeriod is how long a track waits for a requested key
// frame before asking a second time. Browsers ignore a request that arrives
// within 300 ms of their previous key frame (libwebrtc's minimum interval
// between key frames), which happens when the request follows the periodic
// one closely; the second request is then answered.
const keyFrameQuickRetryPeriod = 500 * time.Millisecond

// keyFrameRetryPeriod is how long a track waits between later requests;
// these are logged, because the source may ignore requests altogether.
const keyFrameRetryPeriod = 2 * time.Second

// Bits of OutboundTrack.flags. When none is set, units take a lock-free path.
const (
	flagPaused         uint32 = 1 << iota // drop every unit
	flagWaitKeyFrame                      // drop units until a key frame
	flagPauseAfterNext                    // pause once the next unit is sent
)

// OutboundTrack is an outgoing track.
type OutboundTrack struct {
	Caps webrtc.RTPCodecCapability

	// RequestKeyFrame, when set, asks the source of the stream for a key frame.
	// It is called when the track starts waiting for a key frame (a new
	// reader, a resume, frames discarded for a slow reader), when the reader
	// sends a PLI or FIR, and while still waiting, again after 500 ms and then
	// every 2 seconds. retry is true from the third request on, when the
	// source is probably ignoring requests.
	RequestKeyFrame func(retry bool)

	// placeholder marks the silent audio track that is added to streams
	// without audio, which browsers need to display video.
	placeholder bool

	track      *webrtc.TrackLocalStaticRTP
	ssrc       uint32
	rtcpSender *rtpsender.Sender

	// flags is read on every unit without locking; mutex serializes changes
	// to it and guards keyFrameRequested.
	flags             atomic.Uint32
	mutex             sync.Mutex
	keyFrameRequested time.Time
	keyFrameRequests  int // requests sent while waiting for the current key frame

	// feedback from the reader.
	feedbackMutex sync.Mutex
	srSent        [8]senderReportRecord
	srNext        int
	feedback      OutboundFeedback
	nacks         atomic.Uint64
	plis          atomic.Uint64

	// owned by the goroutine that writes units.
	skipped   bool
	seqOffset uint16
	lastSeq   uint16
	written   bool
}

func (t *OutboundTrack) isVideo() bool {
	return strings.Split(t.Caps.MimeType, "/")[0] == "video"
}

func (t *OutboundTrack) setup(p *PeerConnection) error {
	var trackID string
	if t.isVideo() {
		trackID = "video"
	} else {
		trackID = "audio"
	}

	var err error
	t.track, err = webrtc.NewTrackLocalStaticRTP(
		t.Caps,
		trackID,
		webrtcStreamID,
	)
	if err != nil {
		return err
	}

	sender, err := p.wr.AddTrack(t.track)
	if err != nil {
		return err
	}

	t.ssrc = uint32(sender.GetParameters().Encodings[0].SSRC)

	// a new reader must not start in the middle of a group of pictures,
	// otherwise it decodes frames that reference frames it never got.
	if t.isVideo() {
		t.flags.CompareAndSwap(0, flagWaitKeyFrame)
	}

	t.rtcpSender = &rtpsender.Sender{
		ClockRate: int(t.track.Codec().ClockRate),
		Period:    1 * time.Second,
		TimeNow:   time.Now,
		WritePacketRTCP: func(pkt rtcp.Packet) {
			if sr, ok := pkt.(*rtcp.SenderReport); ok {
				t.recordSenderReport(sr, time.Now())
			}
			p.wr.WriteRTCP([]rtcp.Packet{pkt}) //nolint:errcheck
		},
	}
	t.rtcpSender.Initialize()

	// incoming RTCP packets must always be read to make interceptors work
	go func() {
		buf := make([]byte, 1500)
		for {
			n, _, err2 := sender.Read(buf)
			if err2 != nil {
				return
			}

			pkts, err2 := rtcp.Unmarshal(buf[:n])
			if err2 != nil {
				panic(err2)
			}

			t.processRTCP(pkts, time.Now())
		}
	}()

	return nil
}

func (t *OutboundTrack) close() {
	if t.rtcpSender != nil {
		t.rtcpSender.Close()
	}
}

// WriteRTP writes a RTP packet.
func (t *OutboundTrack) WriteRTP(pkt *rtp.Packet) error {
	return t.WriteRTPWithNTP(pkt, time.Now())
}

func (t *OutboundTrack) requestKeyFrame(retry bool) {
	if t.RequestKeyFrame != nil {
		t.RequestKeyFrame(retry)
	}
}

// startPaused pauses the track from the start. With preview, video sends one
// key frame and audio one unit first, so the reader can show a frame and,
// like any receiver that discovers tracks from incoming packets, learn that
// the track exists.
func (t *OutboundTrack) startPaused(preview bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if preview {
		f := flagPauseAfterNext
		if t.isVideo() {
			f |= flagWaitKeyFrame
		}
		t.flags.Store(f)
	} else {
		t.flags.Store(flagPaused)
	}
}

// setPaused stops or resumes sending. On resume, video waits for the next
// key frame, which is requested straight away.
func (t *OutboundTrack) setPaused(paused bool) {
	t.mutex.Lock()

	f := t.flags.Load() &^ flagPauseAfterNext
	wasPaused := f&flagPaused != 0

	needKeyFrame := false
	switch {
	case paused:
		f |= flagPaused
	case wasPaused:
		f &^= flagPaused
		// frames that follow the pause reference frames the reader never got.
		if t.isVideo() {
			f |= flagWaitKeyFrame
			t.keyFrameRequested = time.Now()
			t.keyFrameRequests = 1
			needKeyFrame = true
		}
	}
	t.flags.Store(f)
	t.mutex.Unlock()

	if needKeyFrame {
		t.requestKeyFrame(false)
	}
}

// isPaused reports whether the track sends nothing.
func (t *OutboundTrack) isPaused() bool {
	return t.flags.Load()&flagPaused != 0
}

// requireKeyFrame makes video wait for the next key frame, after frames were
// discarded because the reader was too slow.
func (t *OutboundTrack) requireKeyFrame() {
	if !t.isVideo() {
		return
	}

	t.mutex.Lock()
	f := t.flags.Load()
	if f&(flagPaused|flagWaitKeyFrame) != 0 {
		t.mutex.Unlock()
		return
	}
	t.flags.Store(f | flagWaitKeyFrame)
	t.keyFrameRequested = time.Now()
	t.keyFrameRequests = 1
	t.mutex.Unlock()

	t.requestKeyFrame(false)
}

// skipUnit reports whether a unit must be dropped instead of being sent.
// It must be called once per unit, before its packets are written, by the
// goroutine that writes units.
func (t *OutboundTrack) skipUnit(keyFrame bool) bool {
	if t.flags.Load() == 0 {
		return false
	}

	t.mutex.Lock()
	skip, request, retry := t.skipUnitSlow(keyFrame, time.Now())
	t.mutex.Unlock()

	if request {
		t.requestKeyFrame(retry)
	}
	if skip {
		t.skipped = true
	}
	return skip
}

func (t *OutboundTrack) skipUnitSlow(keyFrame bool, now time.Time) (skip bool, request bool, retry bool) {
	f := t.flags.Load()

	if f&flagPaused != 0 {
		return true, false, false
	}

	if f&flagWaitKeyFrame != 0 {
		if !keyFrame {
			// a new reader starts waiting without a request: send it now.
			// After that, repeat it if the key frame does not come: once
			// quickly, then every 2 seconds.
			period := keyFrameRetryPeriod
			if t.keyFrameRequests == 1 {
				period = keyFrameQuickRetryPeriod
			}
			switch {
			case t.keyFrameRequested.IsZero():
				t.keyFrameRequested = now
				t.keyFrameRequests = 1
				return true, true, false
			case now.Sub(t.keyFrameRequested) >= period:
				t.keyFrameRequested = now
				t.keyFrameRequests++
				return true, true, t.keyFrameRequests > 2
			}
			return true, false, false
		}
		f &^= flagWaitKeyFrame
		t.keyFrameRequested = time.Time{}
		t.keyFrameRequests = 0
	}

	if f&flagPauseAfterNext != 0 {
		f = (f &^ flagPauseAfterNext) | flagPaused
	}

	t.flags.Store(f)
	return false, false, false
}

// WriteRTPWithNTP writes a RTP packet.
func (t *OutboundTrack) WriteRTPWithNTP(pkt *rtp.Packet, ntp time.Time) error {
	// keep sequence numbers contiguous across skipped units, otherwise the
	// reader counts the packets of skipped units as lost.
	if t.skipped && t.written {
		t.seqOffset = pkt.SequenceNumber - t.lastSeq - 1
	}
	t.skipped = false
	pkt.SequenceNumber -= t.seqOffset
	t.lastSeq = pkt.SequenceNumber
	t.written = true

	// use right SSRC in packet to make rtcpSender work
	pkt.SSRC = t.ssrc

	t.rtcpSender.ProcessPacket(pkt, ntp, true)

	return t.track.WriteRTP(pkt)
}

// OutboundFeedback is what the reader reports about a track, from its RTCP
// receiver reports and feedback messages.
type OutboundFeedback struct {
	// PacketsLost is the cumulative number of packets the reader lost.
	PacketsLost uint64
	// LossRatio is the fraction of packets lost in the last report (0 to 1).
	LossRatio float64
	// Jitter is the interarrival jitter, in seconds.
	Jitter float64
	// RoundTripTime is zero until a report refers to one of our sender reports.
	RoundTripTime time.Duration
	// NACKs is the number of packets the reader asked to be retransmitted.
	NACKs uint64
	// PLIs is the number of key frame requests (PLI and FIR) from the reader.
	PLIs uint64
}

type senderReportRecord struct {
	ntpMiddle uint32 // middle 32 bits of the NTP timestamp, as echoed in LSR
	sent      time.Time
}

// recordSenderReport remembers when a sender report was sent. Its NTP time
// follows the stream rather than the wall clock, so the round-trip time is
// computed from these send times.
func (t *OutboundTrack) recordSenderReport(sr *rtcp.SenderReport, now time.Time) {
	t.feedbackMutex.Lock()
	defer t.feedbackMutex.Unlock()

	t.srSent[t.srNext] = senderReportRecord{ntpMiddle: uint32(sr.NTPTime >> 16), sent: now}
	t.srNext = (t.srNext + 1) % len(t.srSent)
}

func (t *OutboundTrack) processRTCP(pkts []rtcp.Packet, now time.Time) {
	for _, pkt := range pkts {
		switch pkt := pkt.(type) {
		case *rtcp.ReceiverReport:
			for i := range pkt.Reports {
				if pkt.Reports[i].SSRC == t.ssrc {
					t.processReceptionReport(&pkt.Reports[i], now)
				}
			}

		case *rtcp.TransportLayerNack:
			n := 0
			for _, pair := range pkt.Nacks {
				n += len(pair.PacketList())
			}
			t.nacks.Add(uint64(n))

		case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
			t.plis.Add(1)
			// the reader lost a reference frame: pass the request on to the
			// source instead of dropping it.
			t.requestKeyFrame(false)
		}
	}
}

func (t *OutboundTrack) processReceptionReport(rep *rtcp.ReceptionReport, now time.Time) {
	if t.rtcpSender != nil {
		t.rtcpSender.ProcessReceptionReport(rep)
	}

	t.feedbackMutex.Lock()
	defer t.feedbackMutex.Unlock()

	t.feedback.PacketsLost = uint64(rep.TotalLost)
	t.feedback.LossRatio = float64(rep.FractionLost) / 256
	if t.Caps.ClockRate != 0 {
		t.feedback.Jitter = float64(rep.Jitter) / float64(t.Caps.ClockRate)
	}

	if rep.LastSenderReport != 0 {
		for _, r := range t.srSent {
			if r.ntpMiddle == rep.LastSenderReport && !r.sent.IsZero() {
				// DLSR is in units of 1/65536 seconds.
				delay := time.Duration(uint64(rep.Delay) * uint64(time.Second) / 65536)
				if rtt := now.Sub(r.sent) - delay; rtt >= 0 {
					t.feedback.RoundTripTime = rtt
				}
				break
			}
		}
	}
}

// Feedback returns what the reader reported about the track.
func (t *OutboundTrack) Feedback() OutboundFeedback {
	t.feedbackMutex.Lock()
	f := t.feedback
	t.feedbackMutex.Unlock()

	f.NACKs = t.nacks.Load()
	f.PLIs = t.plis.Load()
	return f
}
