package stream

// StreamState represents the state of a KWIK stream
type StreamState int

const (
	StreamStateIdle StreamState = iota
	StreamStateOpen
	StreamStateHalfClosedLocal
	StreamStateHalfClosedRemote
	StreamStateClosed
	StreamStateResetSent
	StreamStateResetReceived
)
