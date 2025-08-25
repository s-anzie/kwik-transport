package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"kwik/internal/utils"
)

// MockSessionLogger implements SessionLogger for testing
type MockSessionLogger struct {
	logs []string
}

func (m *MockSessionLogger) Info(msg string, keysAndValues ...interface{}) {
	m.logs = append(m.logs, fmt.Sprintf("INFO: %s %v", msg, keysAndValues))
}

func (m *MockSessionLogger) Debug(msg string, keysAndValues ...interface{}) {
	m.logs = append(m.logs, fmt.Sprintf("DEBUG: %s %v", msg, keysAndValues))
}

func (m *MockSessionLogger) Warn(msg string, keysAndValues ...interface{}) {
	m.logs = append(m.logs, fmt.Sprintf("WARN: %s %v", msg, keysAndValues))
}

func (m *MockSessionLogger) Error(msg string, keysAndValues ...interface{}) {
	m.logs = append(m.logs, fmt.Sprintf("ERROR: %s %v", msg, keysAndValues))
}

// MockHealthMonitor implements ConnectionHealthMonitor for testing
type MockHealthMonitor struct {
	pathHealth map[string]*PathHealth
}

func (m *MockHealthMonitor) StartMonitoring(sessionID string, paths []string) error {
	if m.pathHealth == nil {
		m.pathHealth = make(map[string]*PathHealth)
	}
	for _, path := range paths {
		m.pathHealth[path] = &PathHealth{
			PathID:       path,
			Status:       PathStatusActive,
			RTT:          10 * time.Millisecond,
			PacketLoss:   0.0,
			LastActivity: time.Now(),
			HealthScore:  100,
		}
	}
	return nil
}

func (m *MockHealthMonitor) StopMonitoring(sessionID string) error {
	return nil
}

func (m *MockHealthMonitor) GetPathHealth(sessionID string, pathID string) *PathHealth {
	if health, exists := m.pathHealth[pathID]; exists {
		return health
	}
	return &PathHealth{
		PathID:      pathID,
		Status:      PathStatusActive,
		RTT:         10 * time.Millisecond,
		HealthScore: 100,
	}
}

func (m *MockHealthMonitor) GetAllPathHealth(sessionID string) map[string]*PathHealth {
	if m.pathHealth == nil {
		return map[string]*PathHealth{
			"default": {
				PathID:      "default",
				Status:      PathStatusActive,
				RTT:         10 * time.Millisecond,
				HealthScore: 100,
			},
		}
	}
	return m.pathHealth
}

func (m *MockHealthMonitor) UpdatePathMetrics(sessionID string, pathID string, metrics PathMetricsUpdate) error {
	if health, exists := m.pathHealth[pathID]; exists {
		if metrics.RTT != nil {
			health.RTT = *metrics.RTT
		}
		if metrics.PacketLost {
			health.PacketLoss += 0.1 // Simulate packet loss increase
		}
		health.LastActivity = time.Now()
	}
	return nil
}

func (m *MockHealthMonitor) SetHealthThresholds(thresholds HealthThresholds) error {
	return nil
}

func (m *MockHealthMonitor) RegisterFailoverCallback(callback FailoverCallback) error {
	return nil
}

func (m *MockHealthMonitor) SetPrimaryPath(sessionID string, pathID string) error {
	if health, exists := m.pathHealth[pathID]; exists {
		// Clear primary flag from all paths
		for _, path := range m.pathHealth {
			path.IsPrimary = false
		}
		// Set the specified path as primary
		health.IsPrimary = true
	}
	return nil
}

func (m *MockHealthMonitor) GetHealthStats() HealthMonitorStats {
	return HealthMonitorStats{}
}

// MockFailoverManager implements FailoverManagerInterface for testing
type MockFailoverManager struct {
	failoverCalls []FailoverCall
}

type FailoverCall struct {
	SessionID string
	Reason    FailoverReason
	Timestamp time.Time
}

func (m *MockFailoverManager) TriggerFailover(sessionID string, reason FailoverReason) error {
	if m.failoverCalls == nil {
		m.failoverCalls = make([]FailoverCall, 0)
	}
	m.failoverCalls = append(m.failoverCalls, FailoverCall{
		SessionID: sessionID,
		Reason:    reason,
		Timestamp: time.Now(),
	})
	return nil
}

// TestErrorRecoveryIntegration tests the complete error recovery system integration
func TestErrorRecoveryIntegration(t *testing.T) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}

	// Create error recovery system
	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, nil)
	defer ers.Close()

	// Test 1: Network timeout error with automatic classification and recovery
	t.Run("NetworkTimeoutRecovery", func(t *testing.T) {
		// Create a network timeout error
		networkErr := NewKwikError(utils.ErrTimeout, "connection timeout: failed to connect to server", fmt.Errorf("timeout"))
		networkErr.WithContext("session_1", "path_1", 0, "network")

		// Attempt recovery
		result, err := ers.RecoverFromError(context.Background(), networkErr)
		if err != nil {
			t.Fatalf("Failed to recover from error: %v", err)
		}

		// Verify recovery was attempted
		if result == nil {
			t.Fatal("Expected recovery result, got nil")
		}

		// Check metrics
		metrics := ers.GetRecoveryMetrics()
		if metrics.TotalRecoveryAttempts == 0 {
			t.Errorf("Expected recovery attempts to be recorded in metrics")
		}
	})

	// Test 2: Authentication error with security classification
	t.Run("AuthenticationErrorHandling", func(t *testing.T) {
		authErr := NewKwikError(utils.ErrAuthenticationFailed, "invalid credentials provided", fmt.Errorf("auth failed"))
		authErr.WithContext("session_2", "path_1", 0, "auth")

		// Verify error classification
		if authErr.Type != ErrorTypeAuthentication {
			t.Errorf("Expected authentication error type, got %v", authErr.Type)
		}

		if authErr.Severity != SeverityCritical {
			t.Errorf("Expected critical severity for auth error, got %v", authErr.Severity)
		}

		// Attempt recovery (should not succeed for auth errors)
		result, err := ers.RecoverFromError(context.Background(), authErr)
		if err != nil {
			t.Fatalf("Failed to process auth error: %v", err)
		}

		// Authentication errors should not trigger automatic recovery
		if result != nil && result.Success {
			t.Errorf("Authentication error should not be recoverable")
		}
	})

	// Test 3: Path failure with automatic failover
	t.Run("PathFailureWithFailover", func(t *testing.T) {
		pathErr := NewKwikError(utils.ErrPathDead, "path became unreachable", fmt.Errorf("path dead"))
		pathErr.WithContext("session_3", "path_2", 0, "path")

		// Verify path classification
		if pathErr.Type != ErrorTypePath {
			t.Errorf("Expected path error type, got %v", pathErr.Type)
		}

		// Attempt recovery
		result, err := ers.RecoverFromError(context.Background(), pathErr)
		if err != nil {
			t.Fatalf("Failed to recover from path error: %v", err)
		}

		// Verify result is not nil
		if result == nil {
			t.Errorf("Expected recovery result, got nil")
		}

		// Wait for processing and recovery
		time.Sleep(100 * time.Millisecond)

		// Check that failover was triggered
		if len(failoverManager.failoverCalls) == 0 {
			t.Errorf("Expected failover to be triggered for path failure")
		} else {
			call := failoverManager.failoverCalls[len(failoverManager.failoverCalls)-1]
			if call.Reason != FailoverReasonPathFailed {
				t.Errorf("Expected FailoverReasonPathFailed, got %v", call.Reason)
			}
		}
	})

	// Test 4: Resource exhaustion with retry strategy
	t.Run("ResourceExhaustionRecovery", func(t *testing.T) {
		resourceErr := NewKwikError(utils.ErrResourceExhausted, "buffer pool exhausted", fmt.Errorf("resource exhausted"))
		resourceErr.WithContext("session_4", "path_1", 123, "buffer")

		// Verify resource classification
		if resourceErr.Type != ErrorTypeResource {
			t.Errorf("Expected resource error type, got %v", resourceErr.Type)
		}

		// Attempt recovery
		result, err := ers.RecoverFromError(context.Background(), resourceErr)
		if err != nil {
			t.Fatalf("Failed to recover from resource error: %v", err)
		}

		// Resource errors should be recoverable
		if result == nil {
			t.Errorf("Expected recovery result for resource error")
		}

		// Check metrics
		metrics := ers.GetRecoveryMetrics()
		if metrics.TotalRecoveryAttempts == 0 {
			t.Errorf("Expected recovery attempts for resource error")
		}
	})
}

// TestErrorClassificationAccuracy tests the accuracy of error classification
func TestErrorClassificationAccuracy(t *testing.T) {
	testCases := []struct {
		name             string
		errorCode        string
		errorMsg         string
		expectedType     ErrorType
		expectedSeverity ErrorSeverity
		metadata         map[string]interface{}
	}{
		{
			name:             "Connection Timeout",
			errorCode:        utils.ErrTimeout,
			errorMsg:         "connection timeout occurred after 30s",
			expectedType:     ErrorTypeTimeout,
			expectedSeverity: SeverityMedium,
			metadata:         map[string]interface{}{"path_id": "primary"},
		},
		{
			name:             "Authentication Failure",
			errorCode:        utils.ErrAuthenticationFailed,
			errorMsg:         "token expired",
			expectedType:     ErrorTypeAuthentication,
			expectedSeverity: SeverityCritical,
			metadata:         map[string]interface{}{"session_id": "test"},
		},
		{
			name:             "Protocol Violation",
			errorCode:        utils.ErrInvalidFrame,
			errorMsg:         "malformed packet header",
			expectedType:     ErrorTypeProtocol,
			expectedSeverity: SeverityLow,
			metadata:         map[string]interface{}{},
		},
		{
			name:             "Resource Exhaustion",
			errorCode:        utils.ErrResourceExhausted,
			errorMsg:         "memory limit exceeded",
			expectedType:     ErrorTypeResource,
			expectedSeverity: SeverityMedium,
			metadata:         map[string]interface{}{"stream_id": uint64(456)},
		},
		{
			name:             "Path Dead",
			errorCode:        utils.ErrPathDead,
			errorMsg:         "no response from endpoint",
			expectedType:     ErrorTypePath,
			expectedSeverity: SeverityLow,
			metadata:         map[string]interface{}{"path_id": "backup"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			kwikErr := NewKwikError(tc.errorCode, tc.errorMsg, fmt.Errorf(tc.errorMsg))

			if kwikErr.Type != tc.expectedType {
				t.Errorf("Expected error type %v, got %v", tc.expectedType, kwikErr.Type)
			}

			if kwikErr.Severity != tc.expectedSeverity {
				t.Errorf("Expected severity %v, got %v", tc.expectedSeverity, kwikErr.Severity)
			}
		})
	}
}

// TestRecoveryStrategySelection tests that appropriate recovery strategies are selected
func TestRecoveryStrategySelection(t *testing.T) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}

	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, nil)
	defer ers.Close()

	testCases := []struct {
		name             string
		errorCode        string
		errorMsg         string
		expectedFailover bool
	}{
		{
			name:             "Network Timeout",
			errorCode:        utils.ErrTimeout,
			errorMsg:         "connection timeout",
			expectedFailover: false,
		},
		{
			name:             "Path Failure",
			errorCode:        utils.ErrPathDead,
			errorMsg:         "path unreachable",
			expectedFailover: true,
		},
		{
			name:             "Protocol Error",
			errorCode:        utils.ErrInvalidFrame,
			errorMsg:         "invalid frame format",
			expectedFailover: false,
		},
		{
			name:             "Resource Exhaustion",
			errorCode:        utils.ErrResourceExhausted,
			errorMsg:         "out of memory",
			expectedFailover: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset failover manager state
			failoverManager.failoverCalls = nil

			kwikErr := NewKwikError(tc.errorCode, tc.errorMsg, fmt.Errorf(tc.errorMsg))
			kwikErr.WithContext("test_session", "test_path", 0, "test")

			// Attempt recovery
			result, err := ers.RecoverFromError(context.Background(), kwikErr)
			if err != nil {
				t.Fatalf("Failed to recover from error: %v", err)
			}

			// Wait for processing
			time.Sleep(50 * time.Millisecond)

			// Check failover expectation
			failoverTriggered := len(failoverManager.failoverCalls) > 0
			if failoverTriggered != tc.expectedFailover {
				t.Errorf("Expected failover: %v, got: %v", tc.expectedFailover, failoverTriggered)
			}

			// Verify result is not nil
			if result == nil {
				t.Errorf("Expected recovery result, got nil")
			}
		})
	}
}

// TestRecoveryMetrics tests recovery metrics collection and reporting
func TestRecoveryMetrics(t *testing.T) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}

	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, nil)
	defer ers.Close()

	// Generate several recovery attempts
	errors := []struct {
		code string
		msg  string
	}{
		{utils.ErrTimeout, "timeout error 1"},
		{utils.ErrPathDead, "path error 1"},
		{utils.ErrResourceExhausted, "resource error 1"},
		{utils.ErrTimeout, "timeout error 2"},
	}

	for i, errInfo := range errors {
		kwikErr := NewKwikError(errInfo.code, errInfo.msg, fmt.Errorf(errInfo.msg))
		kwikErr.WithContext(fmt.Sprintf("session_%d", i), "test_path", uint64(i), "test")

		_, err := ers.RecoverFromError(context.Background(), kwikErr)
		if err != nil {
			t.Fatalf("Failed to recover from error %d: %v", i, err)
		}
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Check metrics
	metrics := ers.GetRecoveryMetrics()

	if metrics.TotalRecoveryAttempts != uint64(len(errors)) {
		t.Errorf("Expected %d total recovery attempts, got %d", len(errors), metrics.TotalRecoveryAttempts)
	}

	if len(metrics.ErrorTypeStats) == 0 {
		t.Errorf("Expected error type statistics to be collected")
	}

	if len(metrics.StrategyStats) == 0 {
		t.Errorf("Expected strategy statistics to be collected")
	}

	if metrics.LastUpdated.IsZero() {
		t.Errorf("Expected metrics to have a last updated timestamp")
	}
}

// TestRecoveryStatusTracking tests recovery status tracking for sessions
func TestRecoveryStatusTracking(t *testing.T) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}

	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, nil)
	defer ers.Close()

	sessionID := "test_session_status"

	// Create and recover from an error
	kwikErr := NewKwikError(utils.ErrTimeout, "test timeout", fmt.Errorf("timeout"))
	kwikErr.WithContext(sessionID, "test_path", 0, "test")

	_, err := ers.RecoverFromError(context.Background(), kwikErr)
	if err != nil {
		t.Fatalf("Failed to recover from error: %v", err)
	}

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	// Check recovery status
	status, err := ers.GetRecoveryStatus(sessionID)
	if err != nil {
		t.Fatalf("Failed to get recovery status: %v", err)
	}

	if status == nil {
		t.Fatal("Expected recovery status, got nil")
	}

	if status.SessionID != sessionID {
		t.Errorf("Expected session ID %s, got %s", sessionID, status.SessionID)
	}

	if status.RecoveryCount == 0 {
		t.Errorf("Expected recovery count > 0, got %d", status.RecoveryCount)
	}

	if status.LastRecovery.IsZero() {
		t.Errorf("Expected last recovery timestamp to be set")
	}
}

// TestConcurrentRecovery tests concurrent recovery operations
func TestConcurrentRecovery(t *testing.T) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}

	config := DefaultRecoveryConfig()
	config.MaxConcurrentRecoveries = 3

	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, config)
	defer ers.Close()

	// Launch multiple concurrent recovery operations
	numConcurrent := 5
	results := make(chan error, numConcurrent)

	for i := 0; i < numConcurrent; i++ {
		go func(index int) {
			kwikErr := NewKwikError(utils.ErrTimeout, fmt.Sprintf("concurrent error %d", index), fmt.Errorf("timeout %d", index))
			kwikErr.WithContext(fmt.Sprintf("session_%d", index), "test_path", uint64(index), "test")

			_, err := ers.RecoverFromError(context.Background(), kwikErr)
			results <- err
		}(i)
	}

	// Wait for all operations to complete
	for i := 0; i < numConcurrent; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("Concurrent recovery %d failed: %v", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Timeout waiting for concurrent recovery %d", i)
		}
	}

	// Check that all recoveries were processed
	metrics := ers.GetRecoveryMetrics()
	if metrics.TotalRecoveryAttempts != uint64(numConcurrent) {
		t.Errorf("Expected %d recovery attempts, got %d", numConcurrent, metrics.TotalRecoveryAttempts)
	}
}

// TestEnhancedErrorClassifierIntegration tests integration with the enhanced error classifier
func TestEnhancedErrorClassifierIntegration(t *testing.T) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}

	// Create error recovery system with enhanced classifier
	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, nil)
	defer ers.Close()

	// Test enhanced classification with context analysis
	t.Run("EnhancedClassificationWithContext", func(t *testing.T) {
		// Create error with enhanced classification
		baseErr := fmt.Errorf("connection timeout occurred")
		kwikErr := NewKwikError(utils.ErrTimeout, "connection timeout occurred", baseErr)
		kwikErr.WithContext("session_enhanced", "path_enhanced", 123, "enhanced_test")

		// Attempt recovery
		result, err := ers.RecoverFromError(context.Background(), kwikErr)
		if err != nil {
			t.Fatalf("Failed to recover from enhanced classified error: %v", err)
		}

		if result == nil {
			t.Fatal("Expected recovery result, got nil")
		}

		// Verify metrics include error type statistics
		metrics := ers.GetRecoveryMetrics()
		if len(metrics.ErrorTypeStats) == 0 {
			t.Errorf("Expected error type statistics to be collected")
		}

		// Check that the error type was properly classified
		found := false
		for errorType := range metrics.ErrorTypeStats {
			if errorType == ErrorTypeNetwork || errorType == ErrorTypeTimeout {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected network or timeout error type in statistics")
		}
	})

	// Test recovery success rate tracking
	t.Run("RecoverySuccessRateTracking", func(t *testing.T) {
		// Generate multiple recovery attempts with different outcomes
		testCases := []struct {
			errorCode     string
			shouldSucceed bool
		}{
			{utils.ErrTimeout, true},               // Recoverable
			{utils.ErrPathDead, true},              // Recoverable with failover
			{utils.ErrResourceExhausted, true},     // Recoverable
			{utils.ErrAuthenticationFailed, false}, // Not recoverable
		}

		initialMetrics := ers.GetRecoveryMetrics()
		initialAttempts := initialMetrics.TotalRecoveryAttempts

		for i, tc := range testCases {
			kwikErr := NewKwikError(tc.errorCode, fmt.Sprintf("Test error %d", i), fmt.Errorf("test"))
			kwikErr.WithContext(fmt.Sprintf("session_rate_%d", i), "test_path", uint64(i), "rate_test")

			result, err := ers.RecoverFromError(context.Background(), kwikErr)
			if err != nil {
				t.Fatalf("Failed to process error %d: %v", i, err)
			}

			// Verify result matches expectation
			if result != nil && result.Success != tc.shouldSucceed {
				t.Errorf("Test case %d: expected success %v, got %v", i, tc.shouldSucceed, result.Success)
			}
		}

		// Check final metrics
		finalMetrics := ers.GetRecoveryMetrics()
		expectedAttempts := initialAttempts + uint64(len(testCases))

		if finalMetrics.TotalRecoveryAttempts != expectedAttempts {
			t.Errorf("Expected %d total attempts, got %d", expectedAttempts, finalMetrics.TotalRecoveryAttempts)
		}

		// Verify success rate is calculated correctly
		if finalMetrics.SuccessRate < 0 || finalMetrics.SuccessRate > 1 {
			t.Errorf("Invalid success rate: %f", finalMetrics.SuccessRate)
		}
	})

	// Test recovery time metrics
	t.Run("RecoveryTimeMetrics", func(t *testing.T) {
		// Create a recoverable error
		kwikErr := NewKwikError(utils.ErrTimeout, "Time metrics test", fmt.Errorf("timeout"))
		kwikErr.WithContext("session_time", "test_path", 0, "time_test")

		startTime := time.Now()
		result, err := ers.RecoverFromError(context.Background(), kwikErr)
		endTime := time.Now()

		if err != nil {
			t.Fatalf("Failed to recover from error: %v", err)
		}

		if result == nil {
			t.Fatal("Expected recovery result, got nil")
		}

		// Verify timing metrics are updated
		finalMetrics := ers.GetRecoveryMetrics()

		if finalMetrics.AverageRecoveryTime <= 0 {
			t.Errorf("Expected positive average recovery time, got %v", finalMetrics.AverageRecoveryTime)
		}

		if finalMetrics.MaxRecoveryTime <= 0 {
			t.Errorf("Expected positive max recovery time, got %v", finalMetrics.MaxRecoveryTime)
		}

		if finalMetrics.MinRecoveryTime <= 0 {
			t.Errorf("Expected positive min recovery time, got %v", finalMetrics.MinRecoveryTime)
		}

		// Verify the recorded duration is reasonable
		actualDuration := endTime.Sub(startTime)
		if result.Duration > actualDuration*2 { // Allow some margin for processing
			t.Errorf("Recorded duration %v seems too high compared to actual %v", result.Duration, actualDuration)
		}
	})
}

// TestRecoverySystemPerformance tests the performance characteristics of the recovery system
func TestRecoverySystemPerformance(t *testing.T) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}

	config := DefaultRecoveryConfig()
	config.MaxConcurrentRecoveries = 10 // Allow more concurrent recoveries for performance test

	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, config)
	defer ers.Close()

	// Test concurrent recovery operations
	t.Run("ConcurrentRecoveryPerformance", func(t *testing.T) {
		numConcurrent := 20
		results := make(chan error, numConcurrent)
		startTime := time.Now()

		// Launch concurrent recovery operations
		for i := 0; i < numConcurrent; i++ {
			go func(index int) {
				kwikErr := NewKwikError(utils.ErrTimeout, fmt.Sprintf("Concurrent error %d", index), fmt.Errorf("timeout %d", index))
				kwikErr.WithContext(fmt.Sprintf("session_perf_%d", index), "test_path", uint64(index), "perf_test")

				_, err := ers.RecoverFromError(context.Background(), kwikErr)
				results <- err
			}(i)
		}

		// Wait for all operations to complete
		for i := 0; i < numConcurrent; i++ {
			select {
			case err := <-results:
				if err != nil {
					t.Errorf("Concurrent recovery %d failed: %v", i, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("Timeout waiting for concurrent recovery %d", i)
			}
		}

		totalTime := time.Since(startTime)

		// Verify all recoveries were processed
		metrics := ers.GetRecoveryMetrics()
		if metrics.TotalRecoveryAttempts < uint64(numConcurrent) {
			t.Errorf("Expected at least %d recovery attempts, got %d", numConcurrent, metrics.TotalRecoveryAttempts)
		}

		// Performance should be reasonable (less than 5 seconds for 20 concurrent operations)
		if totalTime > 5*time.Second {
			t.Errorf("Performance test took too long: %v", totalTime)
		}

		t.Logf("Processed %d concurrent recoveries in %v", numConcurrent, totalTime)
	})

	// Test memory usage with many recovery operations
	t.Run("MemoryUsageTest", func(t *testing.T) {
		initialMetrics := ers.GetRecoveryMetrics()

		// Generate many recovery operations
		numOperations := 100
		for i := 0; i < numOperations; i++ {
			kwikErr := NewKwikError(utils.ErrTimeout, fmt.Sprintf("Memory test error %d", i), fmt.Errorf("timeout %d", i))
			kwikErr.WithContext(fmt.Sprintf("session_mem_%d", i), "test_path", uint64(i), "mem_test")

			_, err := ers.RecoverFromError(context.Background(), kwikErr)
			if err != nil {
				t.Fatalf("Recovery %d failed: %v", i, err)
			}
		}

		finalMetrics := ers.GetRecoveryMetrics()

		// Verify metrics are properly bounded (recent recoveries should be limited)
		if len(finalMetrics.RecentRecoveries) > config.RecentRecoveriesLimit {
			t.Errorf("Recent recoveries list exceeded limit: %d > %d", len(finalMetrics.RecentRecoveries), config.RecentRecoveriesLimit)
		}

		// Verify total count is correct
		expectedTotal := initialMetrics.TotalRecoveryAttempts + uint64(numOperations)
		if finalMetrics.TotalRecoveryAttempts != expectedTotal {
			t.Errorf("Expected %d total attempts, got %d", expectedTotal, finalMetrics.TotalRecoveryAttempts)
		}

		t.Logf("Processed %d operations, recent recoveries list size: %d", numOperations, len(finalMetrics.RecentRecoveries))
	})
}
