package session

import (
	"sync"
	"testing"
	"time"

	"kwik/pkg/protocol"
)

// MockControlHeartbeatSystem implements a mock control heartbeat system for testing
type MockControlHeartbeatSystem struct {
	scheduler interface{}
	stats     map[string]*MockControlHeartbeatStats
	mutex     sync.RWMutex
}

type MockControlHeartbeatStats struct {
	PathID              string
	SessionID           string
	HeartbeatsSent      uint64
	ResponsesReceived   uint64
	ConsecutiveFailures int
	AverageRTT          time.Duration
	CurrentInterval     time.Duration
}

func NewMockControlHeartbeatSystem() *MockControlHeartbeatSystem {
	return &MockControlHeartbeatSystem{
		stats: make(map[string]*MockControlHeartbeatStats),
	}
}

func (m *MockControlHeartbeatSystem) SetHeartbeatScheduler(scheduler interface{}) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.scheduler = scheduler
}

func (m *MockControlHeartbeatSystem) GetAllControlHeartbeatStats() map[string]interface{} {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	result := make(map[string]interface{})
	for k, v := range m.stats {
		result[k] = v
	}
	return result
}

func (m *MockControlHeartbeatSystem) AddMockStats(pathID, sessionID string, rtt time.Duration, failures int) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	m.stats[pathID] = &MockControlHeartbeatStats{
		PathID:              pathID,
		SessionID:           sessionID,
		HeartbeatsSent:      10,
		ResponsesReceived:   10 - uint64(failures),
		ConsecutiveFailures: failures,
		AverageRTT:          rtt,
		CurrentInterval:     30 * time.Second,
	}
}

// MockDataHeartbeatSystem implements a mock data heartbeat system for testing
type MockDataHeartbeatSystem struct {
	scheduler interface{}
	mutex     sync.RWMutex
}

func NewMockDataHeartbeatSystem() *MockDataHeartbeatSystem {
	return &MockDataHeartbeatSystem{}
}

func (m *MockDataHeartbeatSystem) SetHeartbeatScheduler(scheduler interface{}) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.scheduler = scheduler
}

// TestHeartbeatSchedulerIntegration tests the basic integration functionality
func TestHeartbeatSchedulerIntegration(t *testing.T) {
	// Create scheduler and integration
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	// Create mock systems
	controlSystem := NewMockControlHeartbeatSystem()
	dataSystem := NewMockDataHeartbeatSystem()

	// Test integration with control system
	err := integration.IntegrateWithControlSystem(controlSystem)
	if err != nil {
		t.Errorf("Failed to integrate with control system: %v", err)
	}

	// Test integration with data system
	err = integration.IntegrateWithDataSystem(dataSystem)
	if err != nil {
		t.Errorf("Failed to integrate with data system: %v", err)
	}

	// Verify integration status
	status := integration.GetIntegrationStatus()
	if !status["controlIntegrated"].(bool) {
		t.Error("Expected control system to be integrated")
	}
	if !status["dataIntegrated"].(bool) {
		t.Error("Expected data system to be integrated")
	}
	if !status["schedulerAvailable"].(bool) {
		t.Error("Expected scheduler to be available")
	}

	// Start integration
	err = integration.StartIntegration()
	if err != nil {
		t.Errorf("Failed to start integration: %v", err)
	}

	// Verify integration is active
	status = integration.GetIntegrationStatus()
	if !status["active"].(bool) {
		t.Error("Expected integration to be active")
	}
}

// TestNetworkFeedbackIntegration tests network feedback updates through integration
func TestNetworkFeedbackIntegration(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	controlSystem := NewMockControlHeartbeatSystem()
	err := integration.IntegrateWithControlSystem(controlSystem)
	if err != nil {
		t.Errorf("Failed to integrate with control system: %v", err)
	}

	err = integration.StartIntegration()
	if err != nil {
		t.Errorf("Failed to start integration: %v", err)
	}

	pathID := "test-path-feedback"

	// Test network feedback update
	rtt := 150 * time.Millisecond
	packetLoss := 0.05
	consecutiveFailures := 2

	err = integration.UpdateNetworkFeedback(pathID, rtt, packetLoss, consecutiveFailures)
	if err != nil {
		t.Errorf("Failed to update network feedback: %v", err)
	}

	// Verify scheduler received the update
	params := integration.GetSchedulingParameters(pathID)
	if params == nil {
		t.Fatal("Expected scheduling parameters after feedback update")
	}

	// Network condition score should reflect the updated conditions
	if params.NetworkConditionScore < 0 || params.NetworkConditionScore > 1 {
		t.Errorf("Network condition score %f out of range [0, 1]", params.NetworkConditionScore)
	}

	// Recent failure count should be updated
	if params.RecentFailureCount == 0 {
		t.Error("Expected non-zero recent failure count after feedback update")
	}
}

// TestStreamActivityIntegration tests stream activity updates through integration
func TestStreamActivityIntegration(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	dataSystem := NewMockDataHeartbeatSystem()
	err := integration.IntegrateWithDataSystem(dataSystem)
	if err != nil {
		t.Errorf("Failed to integrate with data system: %v", err)
	}

	err = integration.StartIntegration()
	if err != nil {
		t.Errorf("Failed to start integration: %v", err)
	}

	pathID := "test-path-activity"
	streamID := uint64(789)

	// Test stream activity update
	lastActivity := time.Now().Add(-10 * time.Second)
	err = integration.UpdateStreamActivity(streamID, pathID, lastActivity)
	if err != nil {
		t.Errorf("Failed to update stream activity: %v", err)
	}

	// Calculate interval for the stream
	interval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)
	if interval == 0 {
		t.Error("Expected non-zero interval after stream activity update")
	}

	// Verify scheduling parameters include the path
	params := integration.GetSchedulingParameters(pathID)
	if params == nil {
		t.Error("Expected scheduling parameters after stream activity update")
	}
}

// TestRealTimeIntervalUpdates tests that intervals are updated in real-time
func TestRealTimeIntervalUpdates(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	controlSystem := NewMockControlHeartbeatSystem()
	err := integration.IntegrateWithControlSystem(controlSystem)
	if err != nil {
		t.Errorf("Failed to integrate with control system: %v", err)
	}

	err = integration.StartIntegration()
	if err != nil {
		t.Errorf("Failed to start integration: %v", err)
	}

	pathID := "test-path-realtime"

	// Get initial interval
	initialInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)

	// Update network conditions to poor conditions
	err = integration.UpdateNetworkFeedback(pathID, 500*time.Millisecond, 0.2, 5)
	if err != nil {
		t.Errorf("Failed to update network feedback: %v", err)
	}

	// Get updated interval
	updatedInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)

	// Interval should be different (shorter due to poor conditions)
	if updatedInterval >= initialInterval {
		t.Errorf("Expected updated interval (%v) to be shorter than initial (%v) due to poor conditions", updatedInterval, initialInterval)
	}
}

// TestSchedulingParameterPersistence tests parameter persistence functionality
func TestSchedulingParameterPersistence(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	pathID := "test-path-persistence"

	// Initialize some state
	scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)
	scheduler.UpdateNetworkConditions(pathID, 100*time.Millisecond, 0.01)

	// Test persistence (this is a mock implementation)
	err := integration.PersistSchedulingParameters(pathID)
	if err != nil {
		t.Errorf("Failed to persist scheduling parameters: %v", err)
	}

	// Test recovery (this is a mock implementation)
	err = integration.RecoverSchedulingParameters(pathID)
	if err != nil {
		t.Errorf("Failed to recover scheduling parameters: %v", err)
	}
}

// TestIntegrationWithMultiplePaths tests integration with multiple paths
func TestIntegrationWithMultiplePaths(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	controlSystem := NewMockControlHeartbeatSystem()
	dataSystem := NewMockDataHeartbeatSystem()

	err := integration.IntegrateWithControlSystem(controlSystem)
	if err != nil {
		t.Errorf("Failed to integrate with control system: %v", err)
	}

	err = integration.IntegrateWithDataSystem(dataSystem)
	if err != nil {
		t.Errorf("Failed to integrate with data system: %v", err)
	}

	err = integration.StartIntegration()
	if err != nil {
		t.Errorf("Failed to start integration: %v", err)
	}

	// Test multiple paths with different conditions
	paths := []string{"path-1", "path-2", "path-3"}
	rtts := []time.Duration{50 * time.Millisecond, 150 * time.Millisecond, 300 * time.Millisecond}
	losses := []float64{0.0, 0.05, 0.15}

	for i, pathID := range paths {
		err = integration.UpdateNetworkFeedback(pathID, rtts[i], losses[i], i)
		if err != nil {
			t.Errorf("Failed to update network feedback for path %s: %v", pathID, err)
		}

		params := integration.GetSchedulingParameters(pathID)
		if params == nil {
			t.Errorf("Expected scheduling parameters for path %s", pathID)
			continue
		}

		if params.PathID != pathID {
			t.Errorf("Expected pathID %s, got %s", pathID, params.PathID)
		}
	}

	// Verify that paths have different scheduling parameters due to different conditions
	params1 := integration.GetSchedulingParameters(paths[0])
	params3 := integration.GetSchedulingParameters(paths[2])

	if params1.NetworkConditionScore <= params3.NetworkConditionScore {
		t.Error("Expected path with better conditions to have higher network score")
	}
}

// TestIntegrationShutdown tests graceful shutdown of integration
func TestIntegrationShutdown(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)

	controlSystem := NewMockControlHeartbeatSystem()
	dataSystem := NewMockDataHeartbeatSystem()

	// Set up integration
	integration.IntegrateWithControlSystem(controlSystem)
	integration.IntegrateWithDataSystem(dataSystem)
	integration.StartIntegration()

	// Verify integration is active
	status := integration.GetIntegrationStatus()
	if !status["active"].(bool) {
		t.Error("Expected integration to be active before shutdown")
	}

	// Test shutdown
	err := integration.Shutdown()
	if err != nil {
		t.Errorf("Failed to shutdown integration: %v", err)
	}

	// Verify integration is inactive
	status = integration.GetIntegrationStatus()
	if status["active"].(bool) {
		t.Error("Expected integration to be inactive after shutdown")
	}
}

// TestConcurrentAccess tests concurrent access to the integration
func TestConcurrentAccess(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	controlSystem := NewMockControlHeartbeatSystem()
	integration.IntegrateWithControlSystem(controlSystem)
	integration.StartIntegration()

	// Test concurrent updates
	var wg sync.WaitGroup
	numGoroutines := 10
	numUpdates := 100

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			
			pathID := "concurrent-path"
			for j := 0; j < numUpdates; j++ {
				rtt := time.Duration(50+j%100) * time.Millisecond
				packetLoss := float64(j%10) / 100.0
				failures := j % 5

				err := integration.UpdateNetworkFeedback(pathID, rtt, packetLoss, failures)
				if err != nil {
					t.Errorf("Goroutine %d: Failed to update network feedback: %v", goroutineID, err)
				}

				// Occasionally get scheduling parameters
				if j%10 == 0 {
					params := integration.GetSchedulingParameters(pathID)
					if params == nil {
						t.Errorf("Goroutine %d: Expected scheduling parameters", goroutineID)
					}
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify final state is consistent
	params := integration.GetSchedulingParameters("concurrent-path")
	if params == nil {
		t.Error("Expected final scheduling parameters after concurrent access")
	}
}

// BenchmarkIntegrationNetworkFeedback benchmarks network feedback updates through integration
func BenchmarkIntegrationNetworkFeedback(b *testing.B) {
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	controlSystem := NewMockControlHeartbeatSystem()
	integration.IntegrateWithControlSystem(controlSystem)
	integration.StartIntegration()

	pathID := "bench-path"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rtt := time.Duration(50+i%100) * time.Millisecond
		packetLoss := float64(i%10) / 100.0
		failures := i % 5

		integration.UpdateNetworkFeedback(pathID, rtt, packetLoss, failures)
	}
}

// TestRealHeartbeatSystemIntegration tests integration with actual heartbeat system interfaces
func TestRealHeartbeatSystemIntegration(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	// Create a mock that implements the actual heartbeat system interface
	controlSystem := &MockRealControlHeartbeatSystem{
		scheduler: nil,
		intervals: make(map[string]time.Duration),
	}
	
	dataSystem := &MockRealDataHeartbeatSystem{
		scheduler: nil,
		intervals: make(map[string]time.Duration),
	}

	// Test integration
	err := integration.IntegrateWithControlSystem(controlSystem)
	if err != nil {
		t.Errorf("Failed to integrate with real control system: %v", err)
	}

	err = integration.IntegrateWithDataSystem(dataSystem)
	if err != nil {
		t.Errorf("Failed to integrate with real data system: %v", err)
	}

	err = integration.StartIntegration()
	if err != nil {
		t.Errorf("Failed to start integration: %v", err)
	}

	// Verify schedulers were set
	if controlSystem.scheduler == nil {
		t.Error("Expected control system to have scheduler set")
	}
	if dataSystem.scheduler == nil {
		t.Error("Expected data system to have scheduler set")
	}

	// Test real-time interval calculation using the scheduler from integration
	pathID := "test-real-integration"
	
	// Simulate control heartbeat interval calculation
	controlInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)
	if controlInterval == 0 {
		t.Error("Expected non-zero control interval ")
	}

	// Simulate data heartbeat interval calculation
	streamID := uint64(123)
	dataInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)
	if dataInterval == 0 {
		t.Error("Expected non-zero data interval")
	}

	// Test network condition updates
	rtt := 200 * time.Millisecond
	packetLoss := 0.1
	err = scheduler.UpdateNetworkConditions(pathID, rtt, packetLoss)
	if err != nil {
		t.Errorf("Failed to update network conditions: %v", err)
	}

	// Test failure count updates
	err = scheduler.UpdateFailureCount(pathID, 3)
	if err != nil {
		t.Errorf("Failed to update failure count: %v", err)
	}

	// Test stream activity updates
	err = scheduler.UpdateStreamActivity(streamID, pathID, time.Now())
	if err != nil {
		t.Errorf("Failed to update stream activity: %v", err)
	}

	// Verify intervals changed due to poor conditions
	newControlInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)
	if newControlInterval >= controlInterval {
		t.Errorf("Expected control interval to decrease due to poor conditions, got %v >= %v", newControlInterval, controlInterval)
	}
}

// MockRealControlHeartbeatSystem implements the actual interface expected by integration
type MockRealControlHeartbeatSystem struct {
	scheduler interface{}
	intervals map[string]time.Duration
	mutex     sync.RWMutex
}

func (m *MockRealControlHeartbeatSystem) SetHeartbeatScheduler(scheduler interface{}) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.scheduler = scheduler
}

func (m *MockRealControlHeartbeatSystem) GetAllControlHeartbeatStats() map[string]interface{} {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	result := make(map[string]interface{})
	for pathID, interval := range m.intervals {
		result[pathID] = map[string]interface{}{
			"pathID":          pathID,
			"currentInterval": interval,
			"heartbeatsSent":  uint64(10),
			"averageRTT":      100 * time.Millisecond,
		}
	}
	return result
}

// MockRealDataHeartbeatSystem implements the actual interface expected by integration
type MockRealDataHeartbeatSystem struct {
	scheduler interface{}
	intervals map[string]time.Duration
	mutex     sync.RWMutex
}

func (m *MockRealDataHeartbeatSystem) SetHeartbeatScheduler(scheduler interface{}) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.scheduler = scheduler
}

// TestSchedulerParameterPersistenceAndRecovery tests the persistence and recovery functionality
func TestSchedulerParameterPersistenceAndRecovery(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	pathID := "test-persistence"

	// Initialize scheduler state
	scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)
	scheduler.UpdateNetworkConditions(pathID, 150*time.Millisecond, 0.05)
	scheduler.UpdateFailureCount(pathID, 2)

	// Get initial parameters
	initialParams := scheduler.GetSchedulingParams(pathID)
	if initialParams == nil {
		t.Fatal("Expected initial scheduling parameters")
	}

	// Test persistence
	err := integration.PersistSchedulingParameters(pathID)
	if err != nil {
		t.Errorf("Failed to persist parameters: %v", err)
	}

	// Test recovery
	err = integration.RecoverSchedulingParameters(pathID)
	if err != nil {
		t.Errorf("Failed to recover parameters: %v", err)
	}

	// Verify parameters are still accessible
	recoveredParams := scheduler.GetSchedulingParams(pathID)
	if recoveredParams == nil {
		t.Error("Expected recovered scheduling parameters")
	}
}

// TestAdaptiveIntervalCalculationIntegration tests that adaptive intervals work correctly through integration
func TestAdaptiveIntervalCalculationIntegration(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	// Set specific intervals for testing
	config.MinControlInterval = 5 * time.Second
	config.MaxControlInterval = 60 * time.Second
	config.MinDataInterval = 10 * time.Second
	config.MaxDataInterval = 120 * time.Second

	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	controlSystem := &MockRealControlHeartbeatSystem{
		intervals: make(map[string]time.Duration),
	}

	err := integration.IntegrateWithControlSystem(controlSystem)
	if err != nil {
		t.Errorf("Failed to integrate: %v", err)
	}

	err = integration.StartIntegration()
	if err != nil {
		t.Errorf("Failed to start integration: %v", err)
	}

	pathID := "test-adaptive"

	// Test good network conditions
	err = integration.UpdateNetworkFeedback(pathID, 30*time.Millisecond, 0.0, 0)
	if err != nil {
		t.Errorf("Failed to update good network feedback: %v", err)
	}

	goodInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)

	// Test poor network conditions
	err = integration.UpdateNetworkFeedback(pathID, 400*time.Millisecond, 0.2, 5)
	if err != nil {
		t.Errorf("Failed to update poor network feedback: %v", err)
	}

	poorInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)

	// Poor conditions should result in shorter intervals
	if poorInterval >= goodInterval {
		t.Errorf("Expected poor conditions to result in shorter interval, got poor=%v >= good=%v", poorInterval, goodInterval)
	}

	// Verify intervals are within bounds
	if poorInterval < config.MinControlInterval {
		t.Errorf("Poor interval %v below minimum %v", poorInterval, config.MinControlInterval)
	}
	if goodInterval > config.MaxControlInterval {
		t.Errorf("Good interval %v above maximum %v", goodInterval, config.MaxControlInterval)
	}
}

// TestStreamActivityBasedScheduling tests that data plane scheduling considers stream activity
func TestStreamActivityBasedScheduling(t *testing.T) {
	config := DefaultHeartbeatSchedulingConfig()
	config.ActivityThreshold = 30 * time.Second

	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()

	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()

	dataSystem := &MockRealDataHeartbeatSystem{
		intervals: make(map[string]time.Duration),
	}

	err := integration.IntegrateWithDataSystem(dataSystem)
	if err != nil {
		t.Errorf("Failed to integrate: %v", err)
	}

	err = integration.StartIntegration()
	if err != nil {
		t.Errorf("Failed to start integration: %v", err)
	}

	pathID := "test-activity"
	streamID := uint64(456)

	// Get baseline interval
	baselineInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)

	// Update with recent activity
	recentActivity := time.Now().Add(-5 * time.Second)
	err = integration.UpdateStreamActivity(streamID, pathID, recentActivity)
	if err != nil {
		t.Errorf("Failed to update stream activity: %v", err)
	}

	activeInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)

	// Update with old activity
	oldActivity := time.Now().Add(-60 * time.Second)
	err = integration.UpdateStreamActivity(streamID, pathID, oldActivity)
	if err != nil {
		t.Errorf("Failed to update stream activity: %v", err)
	}

	inactiveInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)

	// Active streams should have longer intervals than inactive ones
	if activeInterval <= inactiveInterval {
		t.Errorf("Expected active stream to have longer interval, got active=%v <= inactive=%v", activeInterval, inactiveInterval)
	}

	t.Logf("Baseline: %v, Active: %v, Inactive: %v", baselineInterval, activeInterval, inactiveInterval)
}
