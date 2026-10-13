package webrtc

// Stats are WebRTC statistics.
type Stats struct {
	BytesReceived       uint64
	BytesSent           uint64
	RTPPacketsReceived  uint64
	RTPPacketsSent      uint64
	RTPPacketsLost      uint64
	RTPPacketsJitter    float64
	RTCPPacketsReceived uint64
	RTCPPacketsSent     uint64

	// reported by the remote reader, over all outbound tracks.
	OutboundRTPPacketsLost      uint64
	OutboundRTPPacketsLossRatio float64 // highest of the tracks' last reports
	OutboundRTPPacketsJitter    float64 // highest of the tracks, in seconds
	OutboundRoundTripTime       float64 // in seconds, zero when unknown
	OutboundNACKs               uint64
	OutboundPLIs                uint64
}
