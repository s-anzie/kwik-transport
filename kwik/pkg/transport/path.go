package transport

import (
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"kwik/internal/utils"
)

// PathState represents the detailed state of a path
type PathState int

const (
	PathStateIdle PathState = iota
	PathStateConnecting
	PathStateActive
	PathStateDisconnecting
	PathStateDead
	PathStateError
)

// path implements the Path interface
type path struct {
	id            string
	address       string
	isPrimary     bool
	state         PathState
	connection    quic.Connection
	controlStream quic.Stream
	dataStreams   []quic.Stream
	
	// Metadata for path management
	createdAt     time.Time
	lastActivity  time.Time
	errorCount    int
	lastError     error
	
	// Connection wrapper for KWIK-specific functionality
	wrapper       *connectionWrapper
	
	mutex         sync.RWMutex
}

// ID returns the path identifier
func (p *path) ID() string {
	return p.id
}

// Address returns the server address
func (p *path) Address() string {
	return p.address
}

// IsActive returns whether the path is active
func (p *path) IsActive() bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.state == PathStateActive
}

// IsPrimary returns whether this is the primary path
func (p *path) IsPrimary() bool {
	return p.isPrimary
}

// GetConnection returns the underlying QUIC connection
func (p *path) GetConnection() quic.Connection {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.connection
}

// GetControlStream returns the control plane stream
func (p *path) GetControlStream() (quic.Stream, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	
	// Lazy initialization of control stream
	if p.controlStream == nil && p.connection != nil && p.wrapper != nil {
		stream, err := p.wrapper.CreateControlStream()
		if err != nil {
			return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
				"failed to create control stream", err)
		}
		p.controlStream = stream
	}
	
	if p.controlStream == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"control stream not available", nil)
	}
	
	return p.controlStream, nil
}

// GetDataStreams returns all data plane streams
func (p *path) GetDataStreams() []quic.Stream {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	
	// Return copy to prevent external modification
	streams := make([]quic.Stream, len(p.dataStreams))
	copy(streams, p.dataStreams)
	return streams
}

// Close closes the path and its connection
func (p *path) Close() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	
	p.state = PathStateDead
	
	// Close control stream
	if p.controlStream != nil {
		p.controlStream.Close()
	}
	
	// Close data streams
	for _, stream := range p.dataStreams {
		if stream != nil {
			stream.Close()
		}
	}
	
	// Close QUIC connection
	if p.connection != nil {
		return p.connection.CloseWithError(0, "path closed")
	}
	
	return nil
}

// addDataStream adds a new data stream to this path
func (p *path) addDataStream(stream quic.Stream) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.dataStreams = append(p.dataStreams, stream)
}

// removeDataStream removes a data stream from this path
func (p *path) removeDataStream(stream quic.Stream) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	
	for i, s := range p.dataStreams {
		if s == stream {
			// Remove stream from slice
			p.dataStreams = append(p.dataStreams[:i], p.dataStreams[i+1:]...)
			break
		}
	}
}

// setControlStream sets the control stream for this path
func (p *path) setControlStream(stream quic.Stream) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.controlStream = stream
}

// connectionWrapper implements the ConnectionWrapper interface
type connectionWrapper struct {
	connection quic.Connection
	path       *path
	mutex      sync.RWMutex
}

// newConnectionWrapper creates a new connection wrapper
func newConnectionWrapper(conn quic.Connection, p *path) *connectionWrapper {
	return &connectionWrapper{
		connection: conn,
		path:       p,
	}
}

// GetConnection returns the underlying QUIC connection
func (cw *connectionWrapper) GetConnection() quic.Connection {
	cw.mutex.RLock()
	defer cw.mutex.RUnlock()
	return cw.connection
}

// CreateControlStream creates a control plane stream
func (cw *connectionWrapper) CreateControlStream() (quic.Stream, error) {
	cw.mutex.Lock()
	defer cw.mutex.Unlock()
	
	if cw.connection == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "connection is nil", nil)
	}
	
	// Open control stream (should be stream ID 0)
	stream, err := cw.connection.OpenStreamSync(cw.connection.Context())
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, 
			"failed to create control stream", err)
	}
	
	// Set as control stream for the path
	cw.path.setControlStream(stream)
	
	return stream, nil
}

// CreateDataStream creates a data plane stream
func (cw *connectionWrapper) CreateDataStream() (quic.Stream, error) {
	cw.mutex.Lock()
	defer cw.mutex.Unlock()
	
	if cw.connection == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "connection is nil", nil)
	}
	
	// Open data stream
	stream, err := cw.connection.OpenStreamSync(cw.connection.Context())
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, 
			"failed to create data stream", err)
	}
	
	// Add to path's data streams
	cw.path.addDataStream(stream)
	
	return stream, nil
}

// IsHealthy checks if the connection is healthy
func (cw *connectionWrapper) IsHealthy() bool {
	cw.mutex.RLock()
	defer cw.mutex.RUnlock()
	
	if cw.connection == nil {
		return false
	}
	
	// Check connection context
	select {
	case <-cw.connection.Context().Done():
		return false
	default:
		return true
	}
}

// Close closes the connection wrapper
func (cw *connectionWrapper) Close() error {
	cw.mutex.Lock()
	defer cw.mutex.Unlock()
	
	if cw.connection != nil {
		return cw.connection.CloseWithError(0, "connection wrapper closed")
	}
	
	return nil
}

// Additional utility methods for path state management

// GetState returns the current path state
func (p *path) GetState() PathState {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.state
}

// SetState sets the path state
func (p *path) SetState(state PathState) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.state = state
	p.lastActivity = time.Now()
}

// GetCreatedAt returns when the path was created
func (p *path) GetCreatedAt() time.Time {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.createdAt
}

// GetLastActivity returns the last activity time
func (p *path) GetLastActivity() time.Time {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.lastActivity
}

// UpdateActivity updates the last activity time
func (p *path) UpdateActivity() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.lastActivity = time.Now()
}

// GetErrorCount returns the number of errors encountered
func (p *path) GetErrorCount() int {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.errorCount
}

// IncrementErrorCount increments the error count
func (p *path) IncrementErrorCount(err error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.errorCount++
	p.lastError = err
	p.lastActivity = time.Now()
}

// GetLastError returns the last error encountered
func (p *path) GetLastError() error {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.lastError
}

// GetWrapper returns the connection wrapper
func (p *path) GetWrapper() *connectionWrapper {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.wrapper
}

// IsHealthy checks if the path is healthy
func (p *path) IsHealthy() bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	
	// Check if path is in a healthy state
	if p.state != PathStateActive {
		return false
	}
	
	// Check if connection wrapper is healthy
	if p.wrapper != nil {
		return p.wrapper.IsHealthy()
	}
	
	// Check if connection is still valid
	if p.connection != nil {
		select {
		case <-p.connection.Context().Done():
			return false
		default:
			return true
		}
	}
	
	return false
}

// String returns a string representation of the path state
func (ps PathState) String() string {
	switch ps {
	case PathStateIdle:
		return "IDLE"
	case PathStateConnecting:
		return "CONNECTING"
	case PathStateActive:
		return "ACTIVE"
	case PathStateDisconnecting:
		return "DISCONNECTING"
	case PathStateDead:
		return "DEAD"
	case PathStateError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}