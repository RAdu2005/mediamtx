package stream

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/av1"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/vp8"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/vp9"

	"github.com/bluenviron/mediamtx/internal/unit"
)

// keyFrameIntervalWindow is how many intervals between key frames are
// remembered. The longest of them is reported, so that key frames sent in
// answer to a reader joining do not hide the publisher's own interval.
const keyFrameIntervalWindow = 4

// keyFrameTracker measures the interval between the key frames of a video
// format, which is how long a new reader waits when the publisher cannot be
// asked for a key frame.
type keyFrameTracker struct {
	isKeyFrame func(unit.Payload) bool

	mutex     sync.Mutex
	last      time.Time
	intervals [keyFrameIntervalWindow]time.Duration
	next      int

	longest atomic.Int64
}

func newKeyFrameTracker(forma format.Format) *keyFrameTracker {
	var isKeyFrame func(unit.Payload) bool

	switch forma.(type) {
	case *format.AV1:
		isKeyFrame = func(p unit.Payload) bool { return av1.IsRandomAccess2(p.(unit.PayloadAV1)) }
	case *format.VP9:
		isKeyFrame = func(p unit.Payload) bool { return vp9.IsRandomAccess(p.(unit.PayloadVP9)) }
	case *format.VP8:
		isKeyFrame = func(p unit.Payload) bool { return vp8.IsRandomAccess(p.(unit.PayloadVP8)) }
	case *format.H265:
		isKeyFrame = func(p unit.Payload) bool { return h265.IsRandomAccess(p.(unit.PayloadH265)) }
	case *format.H264:
		isKeyFrame = func(p unit.Payload) bool { return h264.IsRandomAccess(p.(unit.PayloadH264)) }
	default:
		return nil
	}

	return &keyFrameTracker{isKeyFrame: isKeyFrame}
}

func (t *keyFrameTracker) process(payload unit.Payload) {
	if t.isKeyFrame(payload) {
		t.keyFrame(time.Now())
	}
}

func (t *keyFrameTracker) keyFrame(now time.Time) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if !t.last.IsZero() {
		t.intervals[t.next] = now.Sub(t.last)
		t.next = (t.next + 1) % keyFrameIntervalWindow

		var longest time.Duration
		for _, d := range t.intervals {
			longest = max(longest, d)
		}
		t.longest.Store(int64(longest))
	}

	t.last = now
}

// interval returns the longest recent interval, or zero when fewer than two
// key frames were received.
func (t *keyFrameTracker) interval() time.Duration {
	return time.Duration(t.longest.Load())
}
