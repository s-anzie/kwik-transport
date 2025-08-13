package transport

import (
	"time"

	"github.com/quic-go/quic-go"
)

// PathManager manages multiple QUIC connection paths
type PathManager interface {
	// Core path management
	CreatePath(address string) (Path, error)
	CreatePathFromConnection(conn quic.Connection) (Path, error)
	RemovePath(pathID string) error
	GetPath(pathID string) Path
	GetActivePaths() []Path
	GetDeadPaths() []Path
	MarkPathDead(pathID string) error
	
	// Advanced path control
	GetPathCount() int
	GetActivePathCount() int
	GetDeadPathCount() int
	GetPrimaryPath() Path
	SetPrimaryPath(pathID string) error
	CleanupDeadPaths() int
	GetPathsByState(state PathState) []Path
	ValidatePathHealth() []string
	Close() error
	
	// Path failure detection and notification system
	StartHealthMonitoring() error
	StopHealthMonitoring() error
	SetPathStatusNotificationHandler(handler PathStatusNotificationHandler)
	GetPathHealthMetrics(pathID string) (*PathHealthMetrics, error)
	SetHealthCheckInterval(interval time.Duration)
	SetFailureThreshold(threshold int)
}

// PathStatusNotificationHandler handles path status change notifications
type PathStatusNotificationHandler interface {
	OnPathStatusChanged(pathID string, oldStatus, newStatus PathState, metrics *PathHealthMetrics)
	OnPathFailureDetected(pathID string, reason string, metrics *PathHealthMetrics)
	OnPathRecovered(pathID string, metrics *PathHealthMetrics)
}

// PathHealthMetrics contains health metrics for a path
type PathHealthMetrics struct {
	PathID           string
	LastActivity     time.Time
	ErrorCount       int
	LastError        error
	ResponseTime     time.Duration
	PacketLossRate   float64
	ConnectionState  string
	HealthScore      float64
	ConsecutiveFailures int
}

// Path represents a single connection path to a server
type Path interface {
	ID() string
	SetID(id string) // Add SetID method for path ID synchronization
	Address() string
	IsActive() bool
	IsPrimary() bool
	GetConnection() quic.Connection
	GetControlStream() (quic.Stream, error)
	GetDataStreams() []quic.Stream
	Close() error
	
	// New methods for proper control stream management
	CreateControlStreamAsClient() (quic.Stream, error)
	AcceptControlStreamAsServer() (quic.Stream, error)
	
	// Secondary stream support methods
	IsSecondaryPath() bool
	SetSecondaryPath(isSecondary bool)
	AddSecondaryStream(streamID uint64, stream quic.Stream)
	RemoveSecondaryStream(streamID uint64)
	GetSecondaryStream(streamID uint64) (quic.Stream, bool)
	GetSecondaryStreams() map[uint64]quic.Stream
	GetSecondaryStreamCount() int
}

// ConnectionWrapper wraps a QUIC connection with KWIK-specific functionality
type ConnectionWrapper interface {
	GetConnection() quic.Connection
	CreateControlStream() (quic.Stream, error)
	CreateDataStream() (quic.Stream, error)
	IsHealthy() bool
	Close() error
}