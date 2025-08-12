package server

import (
	"fmt"
	"ftpa/internal/types"
	"sync"
	"testing"
	"time"
)

// MockServerConnection implements ServerConnection for testing
type MockServerConnection struct {
	serverID         string
	address          string
	healthy          bool
	lastActivity     time.Time
	connectionStatus ConnectionStatus
	requestCount     int
	shouldFail       bool
	failureCount     int
	mu               sync.RWMutex
}

func NewMockServerConnection(id, address string) *MockServerConnection {
	return &MockServerConnection{
		serverID:         id,
		address:          address,
		healthy:          true,
		lastActivity:     time.Now(),
		connectionStatus: ConnectionStatusConnected,
		requestCount:     0,
		shouldFail:       false,
		failureCount:     0,
	}
}

func (m *MockServerConnection) GetServerID() string {
	return m.serverID
}

func (m *MockServerConnection) GetServerAddress() string {
	return m.address
}

func (m *MockServerConnection) IsHealthy() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.healthy
}

func (m *MockServerConnection) GetLastActivity() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastActivity
}

func (m *MockServerConnection) SendChunkRequest(request *types.ChunkRetryRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.requestCount++
	m.lastActivity = time.Now()
	
	if m.shouldFail {
		m.failureCount++
		return fmt.Errorf("mock server %s failed", m.serverID)
	}
	
	return nil
}

func (m *MockServerConnection) GetConnectionStatus() ConnectionStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connectionStatus
}

func (m *MockServerConnection) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectionStatus = ConnectionStatusDisconnected
	return nil
}

// Test helper methods
func (m *MockServerConnection) SetHealthy(healthy bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthy = healthy
}

func (m *MockServerConnection) SetShouldFail(shouldFail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldFail = shouldFail
}

func (m *MockServerConnection) GetRequestCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.requestCount
}

func (m *MockServerConnection) GetFailureCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.failureCount
}

func TestNewServerFallbackManager(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary1 := NewMockServerConnection("secondary1", "secondary1:8080")
	secondary2 := NewMockServerConnection("secondary2", "secondary2:8080")
	secondaries := []ServerConnection{secondary1, secondary2}

	config := &FallbackConfig{
		MaxConsecutiveFailures: 2,
		HealthCheckInterval:    1 * time.Second,
		FallbackTimeout:        5 * time.Second,
		RetryInterval:          1 * time.Second,
		MaxFallbackAttempts:    3,
		PreferPrimary:          true,
		AutoRecovery:           true,
		RecoveryCheckInterval:  2 * time.Second,
	}

	manager := NewServerFallbackManager(primary, secondaries, config)
	defer manager.Close()

	// Check initial state
	activeServer := manager.GetActiveServer()
	if activeServer.GetServerID() != "primary" {
		t.Errorf("Expected active server to be primary, got %s", activeServer.GetServerID())
	}

	// Check server health initialization
	health := manager.GetServerHealth()
	if len(health) != 3 {
		t.Errorf("Expected 3 servers in health map, got %d", len(health))
	}

	for _, serverID := range []string{"primary", "secondary1", "secondary2"} {
		if h, exists := health[serverID]; !exists {
			t.Errorf("Server %s not found in health map", serverID)
		} else if !h.IsHealthy {
			t.Errorf("Server %s should be healthy initially", serverID)
		}
	}
}

func TestNewServerFallbackManagerWithDefaultConfig(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondaries := []ServerConnection{}

	manager := NewServerFallbackManager(primary, secondaries, nil)
	defer manager.Close()

	// Should use default config
	if manager.fallbackConfig.MaxConsecutiveFailures != 3 {
		t.Errorf("Expected default max consecutive failures 3, got %d", manager.fallbackConfig.MaxConsecutiveFailures)
	}
}

func TestSendChunkRequestSuccess(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondaries := []ServerConnection{}

	manager := NewServerFallbackManager(primary, secondaries, nil)
	defer manager.Close()

	request := &types.ChunkRetryRequest{
		Filename:    "test.txt",
		ChunkID:     1,
		SequenceNum: 0,
		RetryCount:  1,
	}

	err := manager.SendChunkRequest(request)
	if err != nil {
		t.Errorf("Expected successful request, got error: %v", err)
	}

	if primary.GetRequestCount() != 1 {
		t.Errorf("Expected 1 request to primary, got %d", primary.GetRequestCount())
	}
}

func TestSendChunkRequestWithFallback(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	config := &FallbackConfig{
		MaxConsecutiveFailures: 2,
		HealthCheckInterval:    10 * time.Second, // Long interval to avoid interference
		FallbackTimeout:        5 * time.Second,
		RetryInterval:          1 * time.Second,
		MaxFallbackAttempts:    3,
		PreferPrimary:          false, // Disable auto-recovery for this test
		AutoRecovery:           false,
		RecoveryCheckInterval:  10 * time.Second,
	}

	manager := NewServerFallbackManager(primary, secondaries, config)
	defer manager.Close()

	request := &types.ChunkRetryRequest{
		Filename:    "test.txt",
		ChunkID:     1,
		SequenceNum: 0,
		RetryCount:  1,
	}

	// Make primary fail
	primary.SetShouldFail(true)

	// First request should fail but not trigger fallback (need 2 consecutive failures)
	err := manager.SendChunkRequest(request)
	if err == nil {
		t.Errorf("Expected first request to fail")
	}

	// Second request should trigger fallback
	err = manager.SendChunkRequest(request)
	if err != nil {
		t.Errorf("Expected second request to succeed after fallback, got error: %v", err)
	}

	// Check that we switched to secondary
	activeServer := manager.GetActiveServer()
	if activeServer.GetServerID() != "secondary" {
		t.Errorf("Expected active server to be secondary after fallback, got %s", activeServer.GetServerID())
	}

	// Check request counts
	if primary.GetRequestCount() != 2 {
		t.Errorf("Expected 2 requests to primary, got %d", primary.GetRequestCount())
	}

	if secondary.GetRequestCount() != 1 {
		t.Errorf("Expected 1 request to secondary, got %d", secondary.GetRequestCount())
	}
}

func TestFallbackWithAllServersFailing(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	config := &FallbackConfig{
		MaxConsecutiveFailures: 1, // Trigger fallback immediately
		HealthCheckInterval:    10 * time.Second,
		FallbackTimeout:        5 * time.Second,
		RetryInterval:          1 * time.Second,
		MaxFallbackAttempts:    3,
		PreferPrimary:          false,
		AutoRecovery:           false,
		RecoveryCheckInterval:  10 * time.Second,
	}

	manager := NewServerFallbackManager(primary, secondaries, config)
	defer manager.Close()

	request := &types.ChunkRetryRequest{
		Filename:    "test.txt",
		ChunkID:     1,
		SequenceNum: 0,
		RetryCount:  1,
	}

	// Make both servers fail
	primary.SetShouldFail(true)
	secondary.SetShouldFail(true)

	// Request should fail after trying both servers
	err := manager.SendChunkRequest(request)
	if err == nil {
		t.Errorf("Expected request to fail when all servers are failing")
	}

	// Both servers should have been tried
	if primary.GetRequestCount() == 0 {
		t.Errorf("Expected primary to be tried")
	}
}

func TestManualSwitchToServer(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	manager := NewServerFallbackManager(primary, secondaries, nil)
	defer manager.Close()

	// Initially on primary
	activeServer := manager.GetActiveServer()
	if activeServer.GetServerID() != "primary" {
		t.Errorf("Expected active server to be primary initially")
	}

	// Manually switch to secondary
	err := manager.ManualSwitchToServer("secondary")
	if err != nil {
		t.Errorf("Expected successful manual switch, got error: %v", err)
	}

	// Check that we switched
	activeServer = manager.GetActiveServer()
	if activeServer.GetServerID() != "secondary" {
		t.Errorf("Expected active server to be secondary after manual switch, got %s", activeServer.GetServerID())
	}

	// Try to switch to non-existent server
	err = manager.ManualSwitchToServer("nonexistent")
	if err == nil {
		t.Errorf("Expected error when switching to non-existent server")
	}
}

func TestManualSwitchToUnhealthyServer(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	manager := NewServerFallbackManager(primary, secondaries, nil)
	defer manager.Close()

	// Make secondary unhealthy
	secondary.SetHealthy(false)

	// Force a health check to update the server status immediately
	manager.ForceHealthCheck()

	// Try to switch to unhealthy server
	err := manager.ManualSwitchToServer("secondary")
	if err == nil {
		t.Errorf("Expected error when switching to unhealthy server")
	}
}

func TestAutoRecoveryToPrimary(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	config := &FallbackConfig{
		MaxConsecutiveFailures: 1,
		HealthCheckInterval:    50 * time.Millisecond, // Fast health checks
		FallbackTimeout:        5 * time.Second,
		RetryInterval:          1 * time.Second,
		MaxFallbackAttempts:    3,
		PreferPrimary:          true,
		AutoRecovery:           true,
		RecoveryCheckInterval:  100 * time.Millisecond, // Fast recovery checks
	}

	manager := NewServerFallbackManager(primary, secondaries, config)
	defer manager.Close()

	// Manually switch to secondary
	err := manager.ManualSwitchToServer("secondary")
	if err != nil {
		t.Fatalf("Failed to manually switch to secondary: %v", err)
	}

	// Verify we're on secondary
	activeServer := manager.GetActiveServer()
	if activeServer.GetServerID() != "secondary" {
		t.Errorf("Expected to be on secondary server")
	}

	// Wait for auto-recovery to kick in
	time.Sleep(200 * time.Millisecond)

	// Should have recovered to primary
	activeServer = manager.GetActiveServer()
	if activeServer.GetServerID() != "primary" {
		t.Errorf("Expected auto-recovery to primary, still on %s", activeServer.GetServerID())
	}
}

func TestFallbackCallbacks(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	config := &FallbackConfig{
		MaxConsecutiveFailures: 1,
		HealthCheckInterval:    10 * time.Second,
		FallbackTimeout:        5 * time.Second,
		RetryInterval:          1 * time.Second,
		MaxFallbackAttempts:    3,
		PreferPrimary:          false,
		AutoRecovery:           false,
		RecoveryCheckInterval:  10 * time.Second,
	}

	manager := NewServerFallbackManager(primary, secondaries, config)
	defer manager.Close()

	// Set up callback
	var callbackCalled bool
	var callbackEvent *FallbackEvent
	var callbackMutex sync.Mutex

	callback := func(event *FallbackEvent) error {
		callbackMutex.Lock()
		defer callbackMutex.Unlock()
		callbackCalled = true
		callbackEvent = event
		return nil
	}

	manager.AddFallbackCallback(callback)

	// Trigger fallback by making primary fail
	primary.SetShouldFail(true)

	request := &types.ChunkRetryRequest{
		Filename:    "test.txt",
		ChunkID:     1,
		SequenceNum: 0,
		RetryCount:  1,
	}

	// This should trigger fallback and callback
	manager.SendChunkRequest(request)

	// Wait a bit for callback to be called
	time.Sleep(100 * time.Millisecond)

	callbackMutex.Lock()
	called := callbackCalled
	event := callbackEvent
	callbackMutex.Unlock()

	if !called {
		t.Errorf("Expected fallback callback to be called")
	}

	if event != nil {
		if event.EventType != FallbackEventServerFailure {
			t.Errorf("Expected server failure event, got %v", event.EventType)
		}
		if event.FromServer != "primary" {
			t.Errorf("Expected fallback from primary, got %s", event.FromServer)
		}
		if event.ToServer != "secondary" {
			t.Errorf("Expected fallback to secondary, got %s", event.ToServer)
		}
	}
}

func TestGetServerHealth(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	manager := NewServerFallbackManager(primary, secondaries, nil)
	defer manager.Close()

	health := manager.GetServerHealth()

	if len(health) != 2 {
		t.Errorf("Expected 2 servers in health map, got %d", len(health))
	}

	primaryHealth, exists := health["primary"]
	if !exists {
		t.Errorf("Primary server not found in health map")
	} else {
		if primaryHealth.ServerID != "primary" {
			t.Errorf("Expected primary server ID, got %s", primaryHealth.ServerID)
		}
		if !primaryHealth.IsHealthy {
			t.Errorf("Expected primary to be healthy")
		}
		if primaryHealth.Status != ServerStatusHealthy {
			t.Errorf("Expected primary status to be healthy, got %v", primaryHealth.Status)
		}
	}
}

func TestGetFallbackHistory(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	config := &FallbackConfig{
		MaxConsecutiveFailures: 1,
		HealthCheckInterval:    10 * time.Second,
		FallbackTimeout:        5 * time.Second,
		RetryInterval:          1 * time.Second,
		MaxFallbackAttempts:    3,
		PreferPrimary:          false,
		AutoRecovery:           false,
		RecoveryCheckInterval:  10 * time.Second,
	}

	manager := NewServerFallbackManager(primary, secondaries, config)
	defer manager.Close()

	// Initially no history
	history := manager.GetFallbackHistory()
	initialCount := len(history)

	// Trigger fallback
	primary.SetShouldFail(true)
	request := &types.ChunkRetryRequest{
		Filename:    "test.txt",
		ChunkID:     1,
		SequenceNum: 0,
		RetryCount:  1,
	}
	manager.SendChunkRequest(request)

	// Check history
	history = manager.GetFallbackHistory()
	if len(history) <= initialCount {
		t.Errorf("Expected fallback history to increase after fallback")
	}

	// Find the fallback event
	var fallbackEvent *FallbackEvent
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].EventType == FallbackEventServerFailure {
			fallbackEvent = &history[i]
			break
		}
	}

	if fallbackEvent == nil {
		t.Errorf("Expected to find fallback event in history")
	} else {
		if fallbackEvent.FromServer != "primary" {
			t.Errorf("Expected fallback from primary, got %s", fallbackEvent.FromServer)
		}
		if fallbackEvent.ToServer != "secondary" {
			t.Errorf("Expected fallback to secondary, got %s", fallbackEvent.ToServer)
		}
		if !fallbackEvent.Success {
			t.Errorf("Expected successful fallback")
		}
	}
}

func TestGetFallbackStatistics(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	config := &FallbackConfig{
		MaxConsecutiveFailures: 1,
		HealthCheckInterval:    10 * time.Second,
		FallbackTimeout:        5 * time.Second,
		RetryInterval:          1 * time.Second,
		MaxFallbackAttempts:    3,
		PreferPrimary:          false,
		AutoRecovery:           false,
		RecoveryCheckInterval:  10 * time.Second,
	}

	manager := NewServerFallbackManager(primary, secondaries, config)
	defer manager.Close()

	// Get initial stats
	stats := manager.GetFallbackStatistics()
	if stats.CurrentServer != "primary" {
		t.Errorf("Expected current server to be primary, got %s", stats.CurrentServer)
	}

	initialFallbacks := stats.TotalFallbacks

	// Trigger fallback
	primary.SetShouldFail(true)
	request := &types.ChunkRetryRequest{
		Filename:    "test.txt",
		ChunkID:     1,
		SequenceNum: 0,
		RetryCount:  1,
	}
	manager.SendChunkRequest(request)

	// Get updated stats
	stats = manager.GetFallbackStatistics()
	if stats.CurrentServer != "secondary" {
		t.Errorf("Expected current server to be secondary after fallback, got %s", stats.CurrentServer)
	}

	if stats.TotalFallbacks != initialFallbacks+1 {
		t.Errorf("Expected total fallbacks to increase by 1, got %d", stats.TotalFallbacks)
	}

	if stats.SuccessfulFallbacks == 0 {
		t.Errorf("Expected at least one successful fallback")
	}

	// Check server health counts
	if stats.ServerHealthCount[ServerStatusHealthy] == 0 {
		t.Errorf("Expected at least one healthy server")
	}
}

func TestHealthChecks(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	config := &FallbackConfig{
		MaxConsecutiveFailures: 3,
		HealthCheckInterval:    50 * time.Millisecond, // Fast health checks
		FallbackTimeout:        5 * time.Second,
		RetryInterval:          1 * time.Second,
		MaxFallbackAttempts:    3,
		PreferPrimary:          false,
		AutoRecovery:           false,
		RecoveryCheckInterval:  10 * time.Second,
	}

	manager := NewServerFallbackManager(primary, secondaries, config)
	defer manager.Close()

	// Make secondary unhealthy
	secondary.SetHealthy(false)

	// Wait for health checks to run
	time.Sleep(150 * time.Millisecond)

	// Check that secondary is marked as unhealthy
	health := manager.GetServerHealth()
	secondaryHealth, exists := health["secondary"]
	if !exists {
		t.Errorf("Secondary server not found in health map")
	} else if secondaryHealth.IsHealthy {
		t.Errorf("Expected secondary to be marked as unhealthy")
	}
}

func TestConcurrentFallbacks(t *testing.T) {
	primary := NewMockServerConnection("primary", "primary:8080")
	secondary := NewMockServerConnection("secondary", "secondary:8080")
	secondaries := []ServerConnection{secondary}

	config := &FallbackConfig{
		MaxConsecutiveFailures: 1,
		HealthCheckInterval:    10 * time.Second,
		FallbackTimeout:        5 * time.Second,
		RetryInterval:          1 * time.Second,
		MaxFallbackAttempts:    3,
		PreferPrimary:          false,
		AutoRecovery:           false,
		RecoveryCheckInterval:  10 * time.Second,
	}

	manager := NewServerFallbackManager(primary, secondaries, config)
	defer manager.Close()

	// Make primary fail
	primary.SetShouldFail(true)

	var wg sync.WaitGroup
	numGoroutines := 10
	errors := make([]error, numGoroutines)

	// Send concurrent requests
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			request := &types.ChunkRetryRequest{
				Filename:    fmt.Sprintf("test%d.txt", index),
				ChunkID:     uint32(index + 1),
				SequenceNum: uint32(index),
				RetryCount:  1,
			}
			errors[index] = manager.SendChunkRequest(request)
		}(i)
	}

	wg.Wait()

	// All requests should eventually succeed (after fallback)
	successCount := 0
	for _, err := range errors {
		if err == nil {
			successCount++
		}
	}

	if successCount == 0 {
		t.Errorf("Expected at least some requests to succeed after fallback")
	}

	// Should have switched to secondary
	activeServer := manager.GetActiveServer()
	if activeServer.GetServerID() != "secondary" {
		t.Errorf("Expected to be on secondary server after concurrent fallbacks")
	}
}

func TestStringMethods(t *testing.T) {
	// Test ServerStatus.String()
	if ServerStatusHealthy.String() != "HEALTHY" {
		t.Errorf("Expected HEALTHY, got %s", ServerStatusHealthy.String())
	}

	// Test ConnectionStatus.String()
	if ConnectionStatusConnected.String() != "CONNECTED" {
		t.Errorf("Expected CONNECTED, got %s", ConnectionStatusConnected.String())
	}

	// Test FallbackEventType.String()
	if FallbackEventServerFailure.String() != "SERVER_FAILURE" {
		t.Errorf("Expected SERVER_FAILURE, got %s", FallbackEventServerFailure.String())
	}
}