package data

import (
	"sync"

	"kwik/internal/utils"
)

// FlowControllerImpl implements the FlowController interface
// It manages flow control for streams and connections
type FlowControllerImpl struct {
	// Stream-level flow control
	streamWindows map[uint64]*StreamWindow
	streamsMutex  sync.RWMutex

	// Connection-level flow control
	connectionWindows map[string]*ConnectionWindow
	connectionsMutex  sync.RWMutex

	// Configuration
	config *FlowControlConfig
}

// FlowControlConfig holds configuration for flow control
type FlowControlConfig struct {
	DefaultStreamWindow     uint64
	DefaultConnectionWindow uint64
	MaxStreamWindow         uint64
	MaxConnectionWindow     uint64
	WindowUpdateThreshold   float64 // Fraction of window consumed before update
}

// StreamWindow represents flow control state for a stream
type StreamWindow struct {
	StreamID      uint64
	WindowSize    uint64
	BytesConsumed uint64
	MaxWindow     uint64
	IsBlocked     bool
	mutex         sync.RWMutex
}

// ConnectionWindow represents flow control state for a connection
type ConnectionWindow struct {
	PathID        string
	WindowSize    uint64
	BytesConsumed uint64
	MaxWindow     uint64
	IsBlocked     bool
	mutex         sync.RWMutex
}

// NewFlowController creates a new flow controller
func NewFlowController() FlowController {
	return &FlowControllerImpl{
		streamWindows:     make(map[uint64]*StreamWindow),
		connectionWindows: make(map[string]*ConnectionWindow),
		config: &FlowControlConfig{
			DefaultStreamWindow:     65536,  // 64KB
			DefaultConnectionWindow: 262144, // 256KB
			MaxStreamWindow:         1048576, // 1MB
			MaxConnectionWindow:     4194304, // 4MB
			WindowUpdateThreshold:   0.5,     // Update when 50% consumed
		},
	}
}

// UpdateStreamWindow updates the flow control window for a stream
func (fc *FlowControllerImpl) UpdateStreamWindow(streamID uint64, windowSize uint64) error {
	fc.streamsMutex.Lock()
	defer fc.streamsMutex.Unlock()

	window, exists := fc.streamWindows[streamID]
	if !exists {
		// Create new stream window
		window = &StreamWindow{
			StreamID:      streamID,
			WindowSize:    windowSize,
			BytesConsumed: 0,
			MaxWindow:     fc.config.MaxStreamWindow,
			IsBlocked:     false,
		}
		fc.streamWindows[streamID] = window
	} else {
		// Update existing window
		window.mutex.Lock()
		window.WindowSize = windowSize
		if windowSize > window.MaxWindow {
			window.MaxWindow = windowSize
		}
		// Check if stream is no longer blocked
		if window.BytesConsumed < window.WindowSize {
			window.IsBlocked = false
		}
		window.mutex.Unlock()
	}

	return nil
}

// GetStreamWindow returns the current flow control window for a stream
func (fc *FlowControllerImpl) GetStreamWindow(streamID uint64) (uint64, error) {
	fc.streamsMutex.RLock()
	window, exists := fc.streamWindows[streamID]
	fc.streamsMutex.RUnlock()

	if !exists {
		// Return default window size
		return fc.config.DefaultStreamWindow, nil
	}

	window.mutex.RLock()
	defer window.mutex.RUnlock()

	return window.WindowSize - window.BytesConsumed, nil
}

// IsStreamBlocked returns whether a stream is flow control blocked
func (fc *FlowControllerImpl) IsStreamBlocked(streamID uint64) bool {
	fc.streamsMutex.RLock()
	window, exists := fc.streamWindows[streamID]
	fc.streamsMutex.RUnlock()

	if !exists {
		return false // No window means not blocked
	}

	window.mutex.RLock()
	defer window.mutex.RUnlock()

	return window.IsBlocked || window.BytesConsumed >= window.WindowSize
}

// UpdateConnectionWindow updates the flow control window for a connection
func (fc *FlowControllerImpl) UpdateConnectionWindow(pathID string, windowSize uint64) error {
	fc.connectionsMutex.Lock()
	defer fc.connectionsMutex.Unlock()

	window, exists := fc.connectionWindows[pathID]
	if !exists {
		// Create new connection window
		window = &ConnectionWindow{
			PathID:        pathID,
			WindowSize:    windowSize,
			BytesConsumed: 0,
			MaxWindow:     fc.config.MaxConnectionWindow,
			IsBlocked:     false,
		}
		fc.connectionWindows[pathID] = window
	} else {
		// Update existing window
		window.mutex.Lock()
		window.WindowSize = windowSize
		if windowSize > window.MaxWindow {
			window.MaxWindow = windowSize
		}
		// Check if connection is no longer blocked
		if window.BytesConsumed < window.WindowSize {
			window.IsBlocked = false
		}
		window.mutex.Unlock()
	}

	return nil
}

// GetConnectionWindow returns the current flow control window for a connection
func (fc *FlowControllerImpl) GetConnectionWindow(pathID string) (uint64, error) {
	fc.connectionsMutex.RLock()
	window, exists := fc.connectionWindows[pathID]
	fc.connectionsMutex.RUnlock()

	if !exists {
		// Return default window size
		return fc.config.DefaultConnectionWindow, nil
	}

	window.mutex.RLock()
	defer window.mutex.RUnlock()

	return window.WindowSize - window.BytesConsumed, nil
}

// IsConnectionBlocked returns whether a connection is flow control blocked
func (fc *FlowControllerImpl) IsConnectionBlocked(pathID string) bool {
	fc.connectionsMutex.RLock()
	window, exists := fc.connectionWindows[pathID]
	fc.connectionsMutex.RUnlock()

	if !exists {
		return false // No window means not blocked
	}

	window.mutex.RLock()
	defer window.mutex.RUnlock()

	return window.IsBlocked || window.BytesConsumed >= window.WindowSize
}

// ConsumeStreamWindow consumes bytes from a stream's flow control window
func (fc *FlowControllerImpl) ConsumeStreamWindow(streamID uint64, bytes uint64) error {
	fc.streamsMutex.RLock()
	window, exists := fc.streamWindows[streamID]
	fc.streamsMutex.RUnlock()

	if !exists {
		// Create window with default size
		err := fc.UpdateStreamWindow(streamID, fc.config.DefaultStreamWindow)
		if err != nil {
			return err
		}
		return fc.ConsumeStreamWindow(streamID, bytes)
	}

	window.mutex.Lock()
	defer window.mutex.Unlock()

	// Check if consumption would exceed window
	if window.BytesConsumed+bytes > window.WindowSize {
		window.IsBlocked = true
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"stream flow control window exceeded", nil)
	}

	window.BytesConsumed += bytes

	// Check if window is now blocked
	if window.BytesConsumed >= window.WindowSize {
		window.IsBlocked = true
	}

	return nil
}

// ConsumeConnectionWindow consumes bytes from a connection's flow control window
func (fc *FlowControllerImpl) ConsumeConnectionWindow(pathID string, bytes uint64) error {
	fc.connectionsMutex.RLock()
	window, exists := fc.connectionWindows[pathID]
	fc.connectionsMutex.RUnlock()

	if !exists {
		// Create window with default size
		err := fc.UpdateConnectionWindow(pathID, fc.config.DefaultConnectionWindow)
		if err != nil {
			return err
		}
		return fc.ConsumeConnectionWindow(pathID, bytes)
	}

	window.mutex.Lock()
	defer window.mutex.Unlock()

	// Check if consumption would exceed window
	if window.BytesConsumed+bytes > window.WindowSize {
		window.IsBlocked = true
		return utils.NewKwikError(utils.ErrConnectionLost,
			"connection flow control window exceeded", nil)
	}

	window.BytesConsumed += bytes

	// Check if window is now blocked
	if window.BytesConsumed >= window.WindowSize {
		window.IsBlocked = true
	}

	return nil
}

// ExpandStreamWindow expands a stream's flow control window
func (fc *FlowControllerImpl) ExpandStreamWindow(streamID uint64, bytes uint64) error {
	fc.streamsMutex.RLock()
	window, exists := fc.streamWindows[streamID]
	fc.streamsMutex.RUnlock()

	if !exists {
		// Create window with expanded size
		return fc.UpdateStreamWindow(streamID, fc.config.DefaultStreamWindow+bytes)
	}

	window.mutex.Lock()
	defer window.mutex.Unlock()

	// Expand window size
	newWindowSize := window.WindowSize + bytes
	if newWindowSize > window.MaxWindow {
		newWindowSize = window.MaxWindow
	}

	window.WindowSize = newWindowSize

	// Check if stream is no longer blocked
	if window.BytesConsumed < window.WindowSize {
		window.IsBlocked = false
	}

	return nil
}

// ExpandConnectionWindow expands a connection's flow control window
func (fc *FlowControllerImpl) ExpandConnectionWindow(pathID string, bytes uint64) error {
	fc.connectionsMutex.RLock()
	window, exists := fc.connectionWindows[pathID]
	fc.connectionsMutex.RUnlock()

	if !exists {
		// Create window with expanded size
		return fc.UpdateConnectionWindow(pathID, fc.config.DefaultConnectionWindow+bytes)
	}

	window.mutex.Lock()
	defer window.mutex.Unlock()

	// Expand window size
	newWindowSize := window.WindowSize + bytes
	if newWindowSize > window.MaxWindow {
		newWindowSize = window.MaxWindow
	}

	window.WindowSize = newWindowSize

	// Check if connection is no longer blocked
	if window.BytesConsumed < window.WindowSize {
		window.IsBlocked = false
	}

	return nil
}