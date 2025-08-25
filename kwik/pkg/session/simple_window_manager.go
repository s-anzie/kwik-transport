package session

import (
	"fmt"
	"sync"
	"time"
	"kwik/pkg/presentation"
)

// SimpleReceiveWindowManager is a basic implementation of ReceiveWindowManager
type SimpleReceiveWindowManager struct {
	windowSize     uint64
	usedWindow     uint64
	mutex          sync.RWMutex
	windowFullCb   func()
	windowAvailCb  func()
}

// NewSimpleReceiveWindowManager creates a new simple receive window manager
func NewSimpleReceiveWindowManager(initialSize uint64) *SimpleReceiveWindowManager {
	return &SimpleReceiveWindowManager{
		windowSize: initialSize,
		usedWindow: 0,
	}
}

// AllocateWindow allocates window space
func (srwm *SimpleReceiveWindowManager) AllocateWindow(streamID uint64, size uint64) error {
	srwm.mutex.Lock()
	defer srwm.mutex.Unlock()
	if srwm.usedWindow + size > srwm.windowSize {
		if srwm.windowFullCb != nil {
			srwm.windowFullCb()
		}
		return fmt.Errorf("window full: used %d, requested %d, total %d", srwm.usedWindow, size, srwm.windowSize)
	}
	srwm.usedWindow += size
	return nil
}

// ReleaseWindow releases window space
func (srwm *SimpleReceiveWindowManager) ReleaseWindow(streamID uint64, size uint64) error {
	srwm.mutex.Lock()
	defer srwm.mutex.Unlock()
	if srwm.usedWindow >= size {
		srwm.usedWindow -= size
	} else {
		srwm.usedWindow = 0
	}
	if srwm.windowAvailCb != nil {
		srwm.windowAvailCb()
	}
	return nil
}

// SlideWindow slides the window
func (srwm *SimpleReceiveWindowManager) SlideWindow(bytesConsumed uint64) error {
	// For this simple implementation, sliding is the same as releasing
	return srwm.ReleaseWindow(0, bytesConsumed)
}

// SetWindowFullCallback sets the callback for when window becomes full
func (srwm *SimpleReceiveWindowManager) SetWindowFullCallback(callback func()) error {
	srwm.windowFullCb = callback
	return nil
}

// SetWindowAvailableCallback sets the callback for when window becomes available
func (srwm *SimpleReceiveWindowManager) SetWindowAvailableCallback(callback func()) error {
	srwm.windowAvailCb = callback
	return nil
}

// GetAvailableWindow returns the available window space
func (srwm *SimpleReceiveWindowManager) GetAvailableWindow() uint64 {
	srwm.mutex.RLock()
	defer srwm.mutex.RUnlock()
	if srwm.windowSize > srwm.usedWindow {
		return srwm.windowSize - srwm.usedWindow
	}
	return 0
}

// SetWindowSize sets the window size
func (srwm *SimpleReceiveWindowManager) SetWindowSize(size uint64) error {
	srwm.mutex.Lock()
	defer srwm.mutex.Unlock()
	srwm.windowSize = size
	return nil
}

// GetWindowSize returns the current window size
func (srwm *SimpleReceiveWindowManager) GetWindowSize() uint64 {
	srwm.mutex.RLock()
	defer srwm.mutex.RUnlock()
	return srwm.windowSize
}

// GetWindowStatus returns window status
func (srwm *SimpleReceiveWindowManager) GetWindowStatus() *presentation.ReceiveWindowStatus {
	srwm.mutex.RLock()
	defer srwm.mutex.RUnlock()
	
	availableSize := uint64(0)
	if srwm.windowSize > srwm.usedWindow {
		availableSize = srwm.windowSize - srwm.usedWindow
	}
	
	utilization := float64(0)
	if srwm.windowSize > 0 {
		utilization = float64(srwm.usedWindow) / float64(srwm.windowSize)
	}
	
	return &presentation.ReceiveWindowStatus{
		TotalSize:            srwm.windowSize,
		UsedSize:             srwm.usedWindow,
		AvailableSize:        availableSize,
		Utilization:          utilization,
		StreamAllocations:    make(map[uint64]uint64),
		IsBackpressureActive: srwm.usedWindow >= srwm.windowSize,
		LastSlideTime:        time.Now(),
	}
}

// IsWindowFull checks if window is full
func (srwm *SimpleReceiveWindowManager) IsWindowFull() bool {
	srwm.mutex.RLock()
	defer srwm.mutex.RUnlock()
	return srwm.usedWindow >= srwm.windowSize
}

// GetWindowUtilization returns window utilization as a percentage
func (srwm *SimpleReceiveWindowManager) GetWindowUtilization() float64 {
	srwm.mutex.RLock()
	defer srwm.mutex.RUnlock()
	if srwm.windowSize == 0 {
		return 0.0
	}
	return float64(srwm.usedWindow) / float64(srwm.windowSize)
}