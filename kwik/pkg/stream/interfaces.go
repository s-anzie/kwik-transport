package stream

import (
	"github.com/quic-go/quic-go"
)

// StreamMultiplexer manages logical KWIK streams over real QUIC streams
type StreamMultiplexer interface {
	CreateLogicalStream(pathID string) (*LogicalStream, error)
	GetLogicalStream(streamID uint64) (*LogicalStream, error)
	CloseLogicalStream(streamID uint64) error
	GetOptimalRatio() int // 3-4 logical streams per real stream
}

// LogicalStream represents a logical KWIK stream
type LogicalStream struct {
	ID           uint64
	PathID       string
	RealStreamID uint64
	Offset       uint64
	Closed       bool
}

// StreamManager manages the lifecycle of streams
type StreamManager interface {
	OpenStream(pathID string) (*LogicalStream, error)
	AcceptStream() (*LogicalStream, error)
	CloseStream(streamID uint64) error
	GetActiveStreams() []*LogicalStream
}

// StreamPool manages a pool of real QUIC streams for reuse
type StreamPool interface {
	GetStream(pathID string) (quic.Stream, error)
	ReturnStream(pathID string, stream quic.Stream)
	CreateNewStream(pathID string) (quic.Stream, error)
	CleanupPath(pathID string)
}