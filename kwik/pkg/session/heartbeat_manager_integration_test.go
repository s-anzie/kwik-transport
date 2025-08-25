package session

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestHeartbeatManagerIntegration tests the integration between HeartbeatManager and HeartbeatIntegrationLayer
func TestHeartbeatManagerIntegration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create integration layer
	integrationLayer := NewHeartbeatIntegrationLayer(ctx, nil)
	
	// Create heartbeat manager
	config := DefaultHeartbeatConfig()
	config.MinInterval = 100 * time.Millisecond
	config.MaxInterval = 1 * time.Second
	
	manager := NewHeartbeatManager(ctx, config)
	manager.SetIntegrationLayer(integrationLayer)
	
	// Test event synchronization
	sessionID := "test-session"
	pathID := "test-path"
	
	// Mock send callback that tracks calls
	sendCalls := 0
	sendCallback := func(sessionID, pathID string) error {
		sendCalls++
		return nil
	}
	
	// Mock timeout callback
	timeoutCalls := 0
	timeoutCallback := func(sessionID, pathID string, consecutiveFails int) {
		timeoutCalls++
	}
	
	// Start heartbeat monitoring
	err := manager.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	
	// Wait for some heartbeats to be sent (heartbeat manager starts with middle interval)
	time.Sleep(700 * time.Millisecond)
	
	// Verify heartbeats were sent
	if sendCalls == 0 {
		t.Errorf("Expected heartbeats to be sent, got %d calls", sendCalls)
	}
	
	// Simulate receiving a heartbeat response
	err = manager.OnHeartbeatReceived(sessionID, pathID)
	if err != nil {
		t.Errorf("Failed to process heartbeat received: %v", err)
	}
	
	// Get integration stats
	stats := integrationLayer.GetIntegrationStats()
	if stats == nil {
		t.Error("Expected integration stats to be available")
	}
	
	if stats.EventsProcessed == 0 {
		t.Errorf("Expected events to be processed by integration layer, got %d events", stats.EventsProcessed)
	}
	
	// Test integrated heartbeat stats
	heartbeatStats := manager.GetIntegratedHeartbeatStats(sessionID, pathID)
	if heartbeatStats == nil {
		t.Error("Expected integrated heartbeat stats to be available")
	}
	
	// Stop heartbeat monitoring
	err = manager.StopHeartbeat(sessionID, pathID)
	if err != nil {
		t.Errorf("Failed to stop heartbeat: %v", err)
	}
	
	// Shutdown
	err = manager.Shutdown()
	if err != nil {
		t.Errorf("Failed to shutdown manager: %v", err)
	}
}

// TestHeartbeatManagerEventSynchronization tests event synchronization with integration layer
func TestHeartbeatManagerEventSynchronization(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create integration layer
	integrationLayer := NewHeartbeatIntegrationLayer(ctx, nil)
	
	// Create heartbeat manager
	config := DefaultHeartbeatConfig()
	manager := NewHeartbeatManager(ctx, config)
	manager.SetIntegrationLayer(integrationLayer)
	
	sessionID := "test-session"
	pathID := "test-path"
	
	// Test direct event synchronization
	event := &HeartbeatEvent{
		Type:      HeartbeatEventSent,
		PathID:    pathID,
		SessionID: sessionID,
		PlaneType: HeartbeatPlaneControl,
		Timestamp: time.Now(),
		Success:   true,
	}
	
	err := manager.SyncHeartbeatEvent(event)
	if err != nil {
		t.Errorf("Failed to sync heartbeat event: %v", err)
	}
	
	// Wait for event processing
	time.Sleep(100 * time.Millisecond)
	
	// Verify event was processed
	stats := integrationLayer.GetIntegrationStats()
	if stats.EventsProcessed == 0 {
		t.Error("Expected event to be processed")
	}
	
	if stats.EventsByType[HeartbeatEventSent] == 0 {
		t.Error("Expected HeartbeatEventSent to be recorded")
	}
}

// TestHeartbeatManagerTimeoutIntegration tests timeout event integration
func TestHeartbeatManagerTimeoutIntegration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create integration layer
	integrationLayer := NewHeartbeatIntegrationLayer(ctx, nil)
	
	// Create heartbeat manager with short timeout
	config := DefaultHeartbeatConfig()
	config.MinInterval = 50 * time.Millisecond
	config.MaxInterval = 100 * time.Millisecond
	config.TimeoutMultiplier = 2.0
	
	manager := NewHeartbeatManager(ctx, config)
	manager.SetIntegrationLayer(integrationLayer)
	
	sessionID := "test-session"
	pathID := "test-path"
	
	// Mock callbacks
	sendCallback := func(sessionID, pathID string) error {
		return nil // Successful send
	}
	
	timeoutReceived := false
	timeoutCallback := func(sessionID, pathID string, consecutiveFails int) {
		timeoutReceived = true
	}
	
	// Start heartbeat monitoring
	err := manager.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	
	// Wait for timeout to occur (don't call OnHeartbeatReceived)
	time.Sleep(300 * time.Millisecond)
	
	// Verify timeout was detected
	if !timeoutReceived {
		t.Error("Expected timeout callback to be called")
	}
	
	// Verify timeout event was synced to integration layer
	stats := integrationLayer.GetIntegrationStats()
	if stats.EventsByType[HeartbeatEventTimeout] == 0 {
		t.Error("Expected timeout event to be synced to integration layer")
	}
	
	// Cleanup
	manager.StopHeartbeat(sessionID, pathID)
	manager.Shutdown()
}

// TestHeartbeatManagerFailureIntegration tests failure event integration
func TestHeartbeatManagerFailureIntegration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create integration layer
	integrationLayer := NewHeartbeatIntegrationLayer(ctx, nil)
	
	// Create heartbeat manager with short intervals for testing
	config := DefaultHeartbeatConfig()
	config.MinInterval = 100 * time.Millisecond
	config.MaxInterval = 200 * time.Millisecond
	manager := NewHeartbeatManager(ctx, config)
	manager.SetIntegrationLayer(integrationLayer)
	
	sessionID := "test-session"
	pathID := "test-path"
	
	// Mock send callback that fails
	sendCallback := func(sessionID, pathID string) error {
		return fmt.Errorf("send failed")
	}
	
	timeoutCallback := func(sessionID, pathID string, consecutiveFails int) {
		// No-op for this test
	}
	
	// Start heartbeat monitoring
	err := manager.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	
	// Wait for heartbeat send attempt (default interval is middle of min/max)
	time.Sleep(700 * time.Millisecond)
	
	// Verify failure event was synced to integration layer
	stats := integrationLayer.GetIntegrationStats()
	if stats.EventsByType[HeartbeatEventFailure] == 0 {
		t.Error("Expected failure event to be synced to integration layer")
	}
	
	// Cleanup
	manager.StopHeartbeat(sessionID, pathID)
	manager.Shutdown()
}

// TestHeartbeatManagerWithoutIntegration tests that manager works without integration layer
func TestHeartbeatManagerWithoutIntegration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create heartbeat manager without integration layer
	config := DefaultHeartbeatConfig()
	config.MinInterval = 100 * time.Millisecond
	config.MaxInterval = 200 * time.Millisecond
	
	manager := NewHeartbeatManager(ctx, config)
	// Don't set integration layer
	
	sessionID := "test-session"
	pathID := "test-path"
	
	sendCalls := 0
	sendCallback := func(sessionID, pathID string) error {
		sendCalls++
		return nil
	}
	
	timeoutCallback := func(sessionID, pathID string, consecutiveFails int) {
		// No-op
	}
	
	// Start heartbeat monitoring
	err := manager.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	
	// Wait for heartbeats
	time.Sleep(300 * time.Millisecond)
	
	// Verify heartbeats were sent even without integration
	if sendCalls == 0 {
		t.Error("Expected heartbeats to be sent even without integration layer")
	}
	
	// Test event sync without integration layer (should not fail)
	event := &HeartbeatEvent{
		Type:      HeartbeatEventSent,
		PathID:    pathID,
		SessionID: sessionID,
		PlaneType: HeartbeatPlaneControl,
		Timestamp: time.Now(),
		Success:   true,
	}
	
	err = manager.SyncHeartbeatEvent(event)
	if err != nil {
		t.Errorf("SyncHeartbeatEvent should not fail without integration layer: %v", err)
	}
	
	// Cleanup
	manager.StopHeartbeat(sessionID, pathID)
	manager.Shutdown()
}