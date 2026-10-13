package stream

import (
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/unit"
)

func TestKeyFrameTrackerInterval(t *testing.T) {
	tr := newKeyFrameTracker(&format.H264{})
	require.Zero(t, tr.interval())

	start := time.Now()
	tr.keyFrame(start)
	require.Zero(t, tr.interval())

	// the publisher's own interval is 10s.
	tr.keyFrame(start.Add(10 * time.Second))
	require.Equal(t, 10*time.Second, tr.interval())

	// a key frame sent in answer to a reader joining does not hide it.
	tr.keyFrame(start.Add(11 * time.Second))
	tr.keyFrame(start.Add(21 * time.Second))
	require.Equal(t, 10*time.Second, tr.interval())

	// once the window only holds shorter intervals, they are reported.
	for i := range keyFrameIntervalWindow {
		tr.keyFrame(start.Add(21*time.Second + time.Duration(i+1)*time.Second))
	}
	require.Equal(t, time.Second, tr.interval())
}

func TestKeyFrameTrackerFormats(t *testing.T) {
	for _, ca := range []struct {
		name     string
		forma    format.Format
		keyFrame unit.Payload
		delta    unit.Payload
	}{
		{
			"h264",
			&format.H264{},
			unit.PayloadH264{{0x65, 0x88}},
			unit.PayloadH264{{0x41, 0x9a}},
		},
		{
			"h265",
			&format.H265{},
			unit.PayloadH265{{0x26, 0x01, 0xaf}},
			unit.PayloadH265{{0x02, 0x01, 0xd0}},
		},
		{
			"vp8",
			&format.VP8{},
			unit.PayloadVP8{0x10, 0x02, 0x00, 0x9d, 0x01, 0x2a},
			unit.PayloadVP8{0x11, 0x02, 0x00},
		},
		{
			"vp9",
			&format.VP9{},
			unit.PayloadVP9{0x82, 0x49, 0x83, 0x42, 0x00, 0x77, 0xf0, 0x32, 0x34},
			unit.PayloadVP9{0x86, 0x00, 0x40},
		},
		{
			"av1",
			&format.AV1{},
			unit.PayloadAV1{{0x0a, 0x0b, 0x00, 0x00, 0x00, 0x2c, 0xcf, 0x7f, 0x0d, 0xbf, 0xff, 0x30, 0x08}},
			unit.PayloadAV1{{0x32, 0x00}},
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			tr := newKeyFrameTracker(ca.forma)
			require.True(t, tr.isKeyFrame(ca.keyFrame))
			require.False(t, tr.isKeyFrame(ca.delta))
		})
	}

	require.Nil(t, newKeyFrameTracker(&format.Opus{}))
}

func TestStreamKeyFrameInterval(t *testing.T) {
	desc := &description.Session{Medias: []*description.Media{
		{
			Type:    description.MediaTypeAudio,
			Formats: []format.Format{&format.Opus{PayloadTyp: 111, ChannelCount: 2}},
		},
		{
			Type: description.MediaTypeVideo,
			Formats: []format.Format{&format.H264{
				PayloadTyp:        96,
				PacketizationMode: 1,
			}},
		},
	}}

	strm := &Stream{
		OrigDesc:          desc,
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		Parent:            &nilLogger{},
	}
	err := strm.Initialize()
	require.NoError(t, err)
	defer strm.Close()

	subStream := &SubStream{Stream: strm}
	err = subStream.Initialize()
	require.NoError(t, err)

	require.Zero(t, strm.KeyFrameInterval())

	idr := unit.PayloadH264{
		{0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02, 0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9, 0x20},
		{0x68, 0xce, 0x3c, 0x80},
		{0x65, 0x88},
	}

	subStream.WriteUnit(desc.Medias[1], desc.Medias[1].Formats[0], &unit.Unit{Payload: idr})
	subStream.WriteUnit(desc.Medias[1], desc.Medias[1].Formats[0], &unit.Unit{PTS: 3000, Payload: unit.PayloadH264{{0x41, 0x9a}}})
	time.Sleep(50 * time.Millisecond)
	subStream.WriteUnit(desc.Medias[1], desc.Medias[1].Formats[0], &unit.Unit{PTS: 6000, Payload: idr})

	d := strm.KeyFrameInterval()
	require.GreaterOrEqual(t, d, 50*time.Millisecond)
	require.Less(t, d, time.Second)
}
