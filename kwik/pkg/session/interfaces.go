package session

import (
	"context"
	"time"
)

// Session represents a KWIK session that maintains QUIC compatibility
// while managing multiple underlying QUIC connections transparently
type Session interface {
	// QUIC-compatible methods for seamless migration
	OpenStreamSync(ctx context.Context) (Stream, error)
	OpenStream() (Stream, error)
	AcceptStream(ctx context.Context) (Stream, error)

	// Server-specific methods for path management (control plane)
	AddPath(address string) error
	RemovePath(pathID string) error
	GetActivePaths() []PathInfo
	GetDeadPaths() []PathInfo
	GetAllPaths() []PathInfo

	// Raw packet transmission for custom protocols
	SendRawData(data []byte, pathID string, remoteStreamID uint64) error

	Close() error
}

// Stream represents a KWIK stream with QUIC-compatible interface
type Stream interface {
	// QUIC-compatible interface
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error

	// KWIK-specific metadata
	StreamID() uint64
	PathID() string

	// Secondary stream isolation methods
	SetOffset(offset int) error
	GetOffset() int
	SetRemoteStreamID(remoteStreamID uint64) error
	RemoteStreamID() uint64
}

// PathInfo contains information about a connection path
type PathInfo struct {
	PathID     string
	Address    string
	IsPrimary  bool
	Status     PathStatus
	CreatedAt  time.Time
	LastActive time.Time
}

// PathStatus represents the current status of a path
type PathStatus int

const (
	PathStatusActive PathStatus = iota
	PathStatusDegraded
	PathStatusDead
	PathStatusConnecting
	PathStatusDisconnecting
)

// SessionState represents the current state of a session
type SessionState int

const (
	SessionStateConnecting SessionState = iota
	SessionStateAuthenticating
	SessionStateAuthenticated
	SessionStateActive
	SessionStateClosed
)

// Listener represents a KWIK listener that maintains QUIC compatibility
// while providing enhanced session management capabilities
type Listener interface {
	// QUIC-compatible methods for seamless migration
	Accept(ctx context.Context) (Session, error)
	Close() error
	Addr() string

	// KWIK-specific methods for enhanced functionality
	AcceptWithConfig(ctx context.Context, config *SessionConfig) (Session, error)
	SetSessionConfig(config *SessionConfig)
	GetActiveSessionCount() int
}

func (s PathStatus) String() string {
	switch s {
	case PathStatusActive:
		return "ACTIVE"
	case PathStatusDegraded:
		return "DEGRADED"
	case PathStatusDead:
		return "DEAD"
	case PathStatusConnecting:
		return "CONNECTING"
	case PathStatusDisconnecting:
		return "DISCONNECTING"
	default:
		return "UNKNOWN"
	}
}

func (s SessionState) String() string {
	switch s {
	case SessionStateConnecting:
		return "CONNECTING"
	case SessionStateAuthenticating:
		return "AUTHENTICATING"
	case SessionStateAuthenticated:
		return "AUTHENTICATED"
	case SessionStateActive:
		return "ACTIVE"
	case SessionStateClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}
