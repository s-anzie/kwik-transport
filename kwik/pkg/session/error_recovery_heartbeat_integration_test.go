package session

import (
	"context"
	"testing"
	"time"
)

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
	m.failoverCalls = append(m.failoverCalls, FailoverCall{
		SessionID: sessionID,
		Reason:    reason,
		Timestamp: time.Now(),
	})
	return nil
}

func (m *MockFailoverManager) GetFailoverCalls() []FailoverCall {
	return m.failoverCalls
}

func (m *MockFailoverManager) Reset() {
	m.failoverCalls = nil
}

// MockSessionLogger implements SessionLogger for testing
type MockSessionLogger struct{}

func (m *MockSessionLogger) Info(msg string, keysAndValues ...interface{})  {}
func (m *MockSessionLogger) Debug(msg string, keysAndValues ...interface{}) {}
func (m *MockSessionLogger) Warn(msg string, keysAndValues ...interface{})  {}
func (m *MockSessionLogger) Error(msg string, keysAndValues ...interface{}) {}

// TestHeartbeatRecoveryStrategy tests the heartbeat-specific recovery strategy
func TestHeartbeatRecoveryStrategy(t *testing.T) {
	strategy := NewHeartbeatRecoveryStrategy(nil, 3)
	
	// Test timeout error recovery
	timeoutError := &KwikError{
		Code:      "HEARTBEAT_TIMEOUT",
		Message:   "heartbeat timeout",
		Type:      ErrorTypeTimeout,
		Component: "heartbeat",
		SessionID: "test-session",
		PathID:    "test-path",
	}
	
	if !strategy.CanRecover(timeoutError) {
		t.Error("Expected strategy to be able to recover from heartbeat timeout")
	}
	
	recoveryContext := &RecoveryContext{
		Error:         timeoutError,
		SessionID:     "test-session",
		PathID:        "test-path",
		AttemptNumber: 1,
		StartTime:     time.Now(),
		MaxRetries:    3,
	}
	
	result, err := strategy.Recover(context.Background(), recoveryContext)
	if err != nil {
		t.Errorf("Recovery failed: %v", err)
	}
	
	if !result.Success {
		t.Error("Expected recovery to succeed for timeout error")
	}
	
	if len(result.ActionsTaken) == 0 {
		t.Error("Expected recovery actions to be taken")
	}
	
	// Test send error recovery
	sendError := &KwikError{
		Code:      "HEARTBEAT_SEND_ERROR",
		Message:   "failed to send heartbeat",
		Type:      ErrorTypeNetwork,
		Component: "heartbeat",
		SessionID: "test-session",
		PathID:    "test-path",
	}
	
	if !strategy.CanRecover(sendError) {
		t.Error("Expected strategy to be able to recover from heartbeat send error")
	}
	
	recoveryContext.Error = sendError
	result, err = strategy.Recover(context.Background(), recoveryContext)
	if err != nil {
		t.Errorf("Recovery failed: %v", err)
	}
	
	if !result.Success {
		t.Error("Expected recovery to succeed for send error")
	}
	
	// Test protocol error recovery
	protocolError := &KwikError{
		Code:      "HEARTBEAT_SEQUENCE_MISMATCH",
		Message:   "sequence mismatch in heartbeat response",
		Type:      ErrorTypeProtocol,
		Component: "heartbeat",
		SessionID: "test-session",
		PathID:    "test-path",
	}
	
	if !strategy.CanRecover(protocolError) {
		t.Error("Expected strategy to be able to recover from heartbeat protocol error")
	}
	
	recoveryContext.Error = protocolError
	result, err = strategy.Recover(context.Background(), recoveryContext)
	if err != nil {
		t.Errorf("Recovery failed: %v", err)
	}
	
	if !result.Success {
		t.Error("Expected recovery to succeed for protocol error")
	}
	
	// Test non-heartbeat error
	nonHeartbeatError := &KwikError{
		Code:      "GENERIC_ERROR",
		Message:   "generic error",
		Type:      ErrorTypeUnknown,
		Component: "other",
		SessionID: "test-session",
		PathID:    "test-path",
	}
	
	if strategy.CanRecover(nonHeartbeatError) {
		t.Error("Expected strategy to not be able to recover from non-heartbeat error")
	}
}

// TestHeartbeatPathFailoverStrategy tests the heartbeat path failover strategy
func TestHeartbeatPathFailoverStrategy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create mock components
	mockFailoverManager := &MockFailoverManager{}
	healthMonitor := NewConnectionHealthMonitor(ctx)
	
	strategy := NewHeartbeatPathFailoverStrategy(mockFailoverManager, healthMonitor, 3)
	
	sessionID := "test-session"
	pathID := "test-path"
	
	// Start monitoring to create path health
	err := healthMonitor.StartMonitoring(sessionID, []string{pathID})
	if err != nil {
		t.Fatalf("Failed to start health monitoring: %v", err)
	}
	
	// Test with healthy path (should not trigger failover)
	timeoutError := &KwikError{
		Code:      "HEARTBEAT_TIMEOUT",
		Message:   "heartbeat timeout",
		Type:      ErrorTypeTimeout,
		Component: "heartbeat",
		SessionID: sessionID,
		PathID:    pathID,
	}
	
	if !strategy.CanRecover(timeoutError) {
		t.Error("Expected strategy to be able to recover from heartbeat timeout")
	}
	
	recoveryContext := &RecoveryContext{
		Error:         timeoutError,
		SessionID:     sessionID,
		PathID:        pathID,
		AttemptNumber: 1,
		StartTime:     time.Now(),
		MaxRetries:    3,
	}
	
	result, err := strategy.Recover(context.Background(), recoveryContext)
	if err != nil {
		t.Errorf("Recovery failed: %v", err)
	}
	
	// Should not trigger failover for healthy path
	if result.Success {
		t.Error("Expected recovery to not succeed for healthy path (should suggest retry)")
	}
	
	if !result.ShouldRetry {
		t.Error("Expected recovery to suggest retry for healthy path")
	}
	
	if len(mockFailoverManager.GetFailoverCalls()) > 0 {
		t.Error("Expected no failover calls for healthy path")
	}
	
	// Simulate degraded path health
	degradedUpdate := PathMetricsUpdate{
		PacketLost: true,
		Activity:   true,
	}
	
	// Update path metrics multiple times to degrade health
	for i := 0; i < 10; i++ {
		healthMonitor.UpdatePathMetrics(sessionID, pathID, degradedUpdate)
	}
	
	// Wait for health update
	time.Sleep(100 * time.Millisecond)
	
	// Test with degraded path (should trigger failover)
	mockFailoverManager.Reset()
	result, err = strategy.Recover(context.Background(), recoveryContext)
	if err != nil {
		t.Errorf("Recovery failed: %v", err)
	}
	
	// Should trigger failover for degraded path
	if !result.Success {
		t.Error("Expected recovery to succeed for degraded path")
	}
	
	if len(result.PathsChanged) == 0 {
		t.Error("Expected paths to be changed during failover")
	}
	
	failoverCalls := mockFailoverManager.GetFailoverCalls()
	if len(failoverCalls) == 0 {
		t.Error("Expected failover to be triggered for degraded path")
	} else {
		if failoverCalls[0].SessionID != sessionID {
			t.Errorf("Expected failover for session %s, got %s", sessionID, failoverCalls[0].SessionID)
		}
		if failoverCalls[0].Reason != FailoverReasonHealthDegraded {
			t.Errorf("Expected failover reason to be HealthDegraded, got %v", failoverCalls[0].Reason)
		}
	}
	
	// Cleanup
	healthMonitor.StopMonitoring(sessionID)
}

// TestTriggerHeartbeatFailureRecovery tests the helper function for triggering recovery
func TestTriggerHeartbeatFailureRecovery(t *testing.T) {
	// Create error recovery system
	logger := &MockSessionLogger{}
	mockFailoverManager := &MockFailoverManager{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	healthMonitor := NewConnectionHealthMonitor(ctx)
	errorRecovery := NewErrorRecoverySystem(logger, healthMonitor, mockFailoverManager, nil)
	
	sessionID := "test-session"
	pathID := "test-path"
	
	// Test timeout failure
	timeoutFailure := &HeartbeatFailure{
		Type:            HeartbeatFailureTimeout,
		PathID:          pathID,
		SessionID:       sessionID,
		SequenceID:      123,
		FailureCount:    3,
		LastSuccess:     time.Now().Add(-30 * time.Second),
		FailureTime:     time.Now(),
		ErrorMessage:    "heartbeat timeout after 30s",
		Recoverable:     true,
		RecoveryAttempts: 0,
	}
	
	err := TriggerHeartbeatFailureRecovery(errorRecovery, sessionID, pathID, timeoutFailure)
	if err != nil {
		t.Errorf("Failed to trigger heartbeat failure recovery: %v", err)
	}
	
	// Test send error failure
	sendFailure := &HeartbeatFailure{
		Type:            HeartbeatFailureSendError,
		PathID:          pathID,
		SessionID:       sessionID,
		SequenceID:      124,
		FailureCount:    1,
		LastSuccess:     time.Now().Add(-5 * time.Second),
		FailureTime:     time.Now(),
		ErrorMessage:    "failed to send heartbeat",
		Recoverable:     true,
		RecoveryAttempts: 0,
	}
	
	err = TriggerHeartbeatFailureRecovery(errorRecovery, sessionID, pathID, sendFailure)
	if err != nil {
		t.Errorf("Failed to trigger heartbeat failure recovery: %v", err)
	}
	
	// Test with nil error recovery system
	err = TriggerHeartbeatFailureRecovery(nil, sessionID, pathID, timeoutFailure)
	if err == nil {
		t.Error("Expected error when error recovery system is nil")
	}
	
	// Test with nil failure
	err = TriggerHeartbeatFailureRecovery(errorRecovery, sessionID, pathID, nil)
	if err == nil {
		t.Error("Expected error when heartbeat failure is nil")
	}
	
	// Cleanup
	errorRecovery.Close()
}

// TestRegisterHeartbeatRecoveryStrategies tests the registration of heartbeat recovery strategies
func TestRegisterHeartbeatRecoveryStrategies(t *testing.T) {
	// Create error recovery system
	logger := &MockSessionLogger{}
	mockFailoverManager := &MockFailoverManager{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	healthMonitor := NewConnectionHealthMonitor(ctx)
	errorRecovery := NewErrorRecoverySystem(logger, healthMonitor, mockFailoverManager, nil)
	
	// Register heartbeat recovery strategies
	err := RegisterHeartbeatRecoveryStrategies(errorRecovery, nil, mockFailoverManager, healthMonitor)
	if err != nil {
		t.Errorf("Failed to register heartbeat recovery strategies: %v", err)
	}
	
	// Test that strategies are registered by attempting recovery
	timeoutError := &KwikError{
		Code:      "HEARTBEAT_TIMEOUT",
		Message:   "heartbeat timeout",
		Type:      ErrorTypeTimeout,
		Component: "heartbeat",
		SessionID: "test-session",
		PathID:    "test-path",
	}
	
	result, err := errorRecovery.RecoverFromError(context.Background(), timeoutError)
	if err != nil {
		t.Errorf("Recovery failed: %v", err)
	}
	
	if result == nil {
		t.Error("Expected recovery result")
	}
	
	// Test with nil error recovery system
	err = RegisterHeartbeatRecoveryStrategies(nil, nil, mockFailoverManager, healthMonitor)
	if err == nil {
		t.Error("Expected error when error recovery system is nil")
	}
	
	// Cleanup
	errorRecovery.Close()
	healthMonitor.StopMonitoring("test-session")
}

// TestErrorRecoveryIntegrationWithHeartbeatSystem tests full integration
func TestErrorRecoveryIntegrationWithHeartbeatSystem(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create integration layer
	integrationLayer := NewHeartbeatIntegrationLayer(ctx, nil)
	
	// Create error recovery system
	logger := &MockSessionLogger{}
	mockFailoverManager := &MockFailoverManager{}
	healthMonitor := NewConnectionHealthMonitor(ctx)
	errorRecovery := NewErrorRecoverySystem(logger, healthMonitor, mockFailoverManager, nil)
	
	// Initialize integration
	err := integrationLayer.InitializeIntegration(nil, healthMonitor, errorRecovery)
	if err != nil {
		t.Errorf("Failed to initialize integration: %v", err)
	}
	
	// Register heartbeat recovery strategies
	err = RegisterHeartbeatRecoveryStrategies(errorRecovery, nil, mockFailoverManager, healthMonitor)
	if err != nil {
		t.Errorf("Failed to register heartbeat recovery strategies: %v", err)
	}
	
	sessionID := "test-session"
	pathID := "test-path"
	
	// Start health monitoring
	err = healthMonitor.StartMonitoring(sessionID, []string{pathID})
	if err != nil {
		t.Fatalf("Failed to start health monitoring: %v", err)
	}
	
	// Test heartbeat failure recovery through integration layer
	failure := &HeartbeatFailure{
		Type:            HeartbeatFailureTimeout,
		PathID:          pathID,
		SessionID:       sessionID,
		SequenceID:      123,
		FailureCount:    3,
		LastSuccess:     time.Now().Add(-30 * time.Second),
		FailureTime:     time.Now(),
		ErrorMessage:    "heartbeat timeout after 30s",
		Recoverable:     true,
		RecoveryAttempts: 0,
	}
	
	err = integrationLayer.TriggerHeartbeatFailureRecovery(pathID, failure)
	if err != nil {
		t.Errorf("Failed to trigger heartbeat failure recovery through integration layer: %v", err)
	}
	
	// Wait for recovery processing
	time.Sleep(100 * time.Millisecond)
	
	// Verify integration stats
	stats := integrationLayer.GetIntegrationStats()
	if stats.RecoveryTriggers == 0 {
		t.Error("Expected recovery triggers to be recorded")
	}
	
	// Test heartbeat event that triggers recovery
	timeoutEvent := &HeartbeatEvent{
		Type:         HeartbeatEventTimeout,
		PathID:       pathID,
		SessionID:    sessionID,
		PlaneType:    HeartbeatPlaneControl,
		SequenceID:   124,
		Timestamp:    time.Now(),
		Success:      false,
		ErrorCode:    "TIMEOUT",
		ErrorMessage: "heartbeat timeout",
	}
	
	err = integrationLayer.SyncHeartbeatEvent(timeoutEvent)
	if err != nil {
		t.Errorf("Failed to sync heartbeat timeout event: %v", err)
	}
	
	// Wait for event processing
	time.Sleep(100 * time.Millisecond)
	
	// Verify event was processed
	stats = integrationLayer.GetIntegrationStats()
	if stats.EventsByType[HeartbeatEventTimeout] == 0 {
		t.Error("Expected timeout event to be processed")
	}
	
	// Cleanup
	integrationLayer.Shutdown()
	errorRecovery.Close()
	healthMonitor.StopMonitoring(sessionID)
}