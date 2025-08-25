package presentation

import (
	"fmt"
	"sync"
	"time"
)

// ResourceReservation represents a reserved resource allocation
type ResourceReservation struct {
	StreamID     uint64
	WindowSize   uint64
	BufferSize   uint64
	Priority     StreamPriority
	ReservedAt   time.Time
	ExpiresAt    time.Time
	ReservationID string
}

// ResourceReservationManager manages resource reservations for stream creation
type ResourceReservationManager struct {
	// Active reservations
	reservations map[string]*ResourceReservation
	reservationsMutex sync.RWMutex
	
	// Resource tracking
	reservedWindowSpace uint64
	reservedStreams     int
	resourceMutex       sync.RWMutex
	
	// Configuration
	reservationTimeout time.Duration
	maxReservations    int
	
	// Cleanup
	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
	cleanupWg     sync.WaitGroup
}

// NewResourceReservationManager creates a new resource reservation manager
func NewResourceReservationManager(reservationTimeout time.Duration, maxReservations int) *ResourceReservationManager {
	rrm := &ResourceReservationManager{
		reservations:       make(map[string]*ResourceReservation),
		reservationTimeout: reservationTimeout,
		maxReservations:    maxReservations,
		stopCleanup:        make(chan struct{}),
	}
	
	// Start cleanup routine
	rrm.cleanupTicker = time.NewTicker(reservationTimeout / 2)
	rrm.cleanupWg.Add(1)
	go rrm.cleanupExpiredReservations()
	
	return rrm
}

// ReserveResources reserves resources for stream creation
func (rrm *ResourceReservationManager) ReserveResources(streamID uint64, windowSize, bufferSize uint64, priority StreamPriority) (*ResourceReservation, error) {
	rrm.reservationsMutex.Lock()
	defer rrm.reservationsMutex.Unlock()
	
	// Check if we have too many reservations
	if len(rrm.reservations) >= rrm.maxReservations {
		return nil, fmt.Errorf("too many active reservations (%d)", len(rrm.reservations))
	}
	
	// Generate reservation ID
	reservationID := fmt.Sprintf("res_%d_%d", streamID, time.Now().UnixNano())
	
	// Create reservation
	reservation := &ResourceReservation{
		StreamID:      streamID,
		WindowSize:    windowSize,
		BufferSize:    bufferSize,
		Priority:      priority,
		ReservedAt:    time.Now(),
		ExpiresAt:     time.Now().Add(rrm.reservationTimeout),
		ReservationID: reservationID,
	}
	
	// Update resource tracking
	rrm.resourceMutex.Lock()
	rrm.reservedWindowSpace += windowSize
	rrm.reservedStreams++
	rrm.resourceMutex.Unlock()
	
	// Store reservation
	rrm.reservations[reservationID] = reservation
	
	return reservation, nil
}

// CommitReservation commits a reservation and removes it from the active list
func (rrm *ResourceReservationManager) CommitReservation(reservationID string) error {
	rrm.reservationsMutex.Lock()
	defer rrm.reservationsMutex.Unlock()
	
	reservation, exists := rrm.reservations[reservationID]
	if !exists {
		return fmt.Errorf("reservation %s not found", reservationID)
	}
	
	// Check if reservation has expired
	if time.Now().After(reservation.ExpiresAt) {
		// Clean up expired reservation
		rrm.releaseReservationLocked(reservationID)
		return fmt.Errorf("reservation %s has expired", reservationID)
	}
	
	// Update resource tracking - remove from reserved since resources are now committed
	rrm.resourceMutex.Lock()
	rrm.reservedWindowSpace -= reservation.WindowSize
	rrm.reservedStreams--
	rrm.resourceMutex.Unlock()
	
	// Remove from reservations (resources are now committed)
	delete(rrm.reservations, reservationID)
	
	return nil
}

// ReleaseReservation releases a reservation and frees the reserved resources
func (rrm *ResourceReservationManager) ReleaseReservation(reservationID string) error {
	rrm.reservationsMutex.Lock()
	defer rrm.reservationsMutex.Unlock()
	
	return rrm.releaseReservationLocked(reservationID)
}

// releaseReservationLocked releases a reservation (must be called with reservationsMutex held)
func (rrm *ResourceReservationManager) releaseReservationLocked(reservationID string) error {
	reservation, exists := rrm.reservations[reservationID]
	if !exists {
		return fmt.Errorf("reservation %s not found", reservationID)
	}
	
	// Update resource tracking
	rrm.resourceMutex.Lock()
	rrm.reservedWindowSpace -= reservation.WindowSize
	rrm.reservedStreams--
	rrm.resourceMutex.Unlock()
	
	// Remove reservation
	delete(rrm.reservations, reservationID)
	
	return nil
}

// GetReservedResources returns the currently reserved resources
func (rrm *ResourceReservationManager) GetReservedResources() (windowSpace uint64, streams int) {
	rrm.resourceMutex.RLock()
	defer rrm.resourceMutex.RUnlock()
	
	return rrm.reservedWindowSpace, rrm.reservedStreams
}

// GetReservationStats returns statistics about reservations
func (rrm *ResourceReservationManager) GetReservationStats() *ReservationStats {
	rrm.reservationsMutex.RLock()
	defer rrm.reservationsMutex.RUnlock()
	
	rrm.resourceMutex.RLock()
	defer rrm.resourceMutex.RUnlock()
	
	activeReservations := len(rrm.reservations)
	expiredCount := 0
	
	now := time.Now()
	for _, reservation := range rrm.reservations {
		if now.After(reservation.ExpiresAt) {
			expiredCount++
		}
	}
	
	return &ReservationStats{
		ActiveReservations:   activeReservations,
		ExpiredReservations:  expiredCount,
		ReservedWindowSpace:  rrm.reservedWindowSpace,
		ReservedStreams:      rrm.reservedStreams,
		MaxReservations:      rrm.maxReservations,
		ReservationTimeout:   rrm.reservationTimeout,
		LastUpdate:           time.Now(),
	}
}

// cleanupExpiredReservations periodically cleans up expired reservations
func (rrm *ResourceReservationManager) cleanupExpiredReservations() {
	defer rrm.cleanupWg.Done()
	
	for {
		select {
		case <-rrm.cleanupTicker.C:
			rrm.performCleanup()
		case <-rrm.stopCleanup:
			return
		}
	}
}

// performCleanup removes expired reservations
func (rrm *ResourceReservationManager) performCleanup() {
	rrm.reservationsMutex.Lock()
	defer rrm.reservationsMutex.Unlock()
	
	now := time.Now()
	var expiredIDs []string
	
	// Find expired reservations
	for id, reservation := range rrm.reservations {
		if now.After(reservation.ExpiresAt) {
			expiredIDs = append(expiredIDs, id)
		}
	}
	
	// Remove expired reservations
	for _, id := range expiredIDs {
		rrm.releaseReservationLocked(id)
	}
}

// Shutdown stops the reservation manager
func (rrm *ResourceReservationManager) Shutdown() error {
	if rrm.cleanupTicker != nil {
		rrm.cleanupTicker.Stop()
	}
	
	close(rrm.stopCleanup)
	rrm.cleanupWg.Wait()
	
	// Clear all reservations
	rrm.reservationsMutex.Lock()
	rrm.reservations = make(map[string]*ResourceReservation)
	rrm.reservationsMutex.Unlock()
	
	rrm.resourceMutex.Lock()
	rrm.reservedWindowSpace = 0
	rrm.reservedStreams = 0
	rrm.resourceMutex.Unlock()
	
	return nil
}

