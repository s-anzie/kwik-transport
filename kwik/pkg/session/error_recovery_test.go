package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"kwik/internal/utils"
)

// TestErrorRecoverySystem_BasicOperations tests basic error recovery system operations
func TestErrorRecoverySystem_BasicOperations(t *testing.T) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}
	
	config := DefaultRecoveryConfig()
	config.MaxConcurrentRecoveries = 2

	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, config)

	// Test registering a strategy
	strategy := NewRetryWithBackoffStrategy(3, 100*time.Millisecond, 2.0)
	err := ers.RegisterStrategy(ErrorTypeNetwork, strategy)
	if err != nil {
		t.Fatalf("Failed to register strategy: %v", err)
	}

	// Test getting metrics
	metrics := ers.GetRecoveryMetrics()
	if metrics == nil {
		t.Errorf("Expected metrics, got nil")
	}

	// Test closing the system
	err = ers.Close()
	if err != nil {
		t.Fatalf("Failed to close error recovery system: %v", err)
	}
}

func TestErrorRecoverySystem_RecoverFromError(t *testing.T) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}
	
	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, nil)
	defer ers.Close()

	// Create test error
	kwikError := NewKwikError(utils.ErrTimeout, "Test timeout error", nil)
	kwikError.WithContext("session_1", "path_1", 123, "test_component")

	// Test recovery
	ctx := context.Background()
	result, err := ers.RecoverFromError(ctx, kwikError)
	
	if err != nil {
		t.Fatalf("Recovery failed: %v", err)
	}

	if result == nil {
		t.Fatalf("Expected recovery result, got nil")
	}

	if !result.Success {
		t.Errorf("Expected successful recovery, got failure: %v", result.Error)
	}

	if result.Strategy != StrategyTypeRetryWithBackoff {
		t.Errorf("Expected retry strategy, got %v", result.Strategy)
	}
}

func TestErrorRecoverySystem_PathFailover(t *testing.T) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}
	
	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, nil)
	defer ers.Close()

	// Create path error
	kwikError := NewKwikError(utils.ErrPathDead, "Path is dead", nil)
	kwikError.WithContext("session_1", "path_1", 0, "path_manager")

	// Test recovery
	ctx := context.Background()
	result, err := ers.RecoverFromError(ctx, kwikError)
	
	if err != nil {
		t.Fatalf("Recovery failed: %v", err)
	}

	if result == nil {
		t.Fatalf("Expected recovery result, got nil")
	}

	if !result.Success {
		t.Errorf("Expected successful recovery, got failure: %v", result.Error)
	}

	// Check that failover was triggered
	if len(failoverManager.failoverCalls) != 1 {
		t.Errorf("Expected 1 failover call, got %d", len(failoverManager.failoverCalls))
	}

	expectedCall := FailoverCall{
		SessionID: "session_1",
		Reason:    FailoverReasonPathFailed,
	}
	if failoverManager.failoverCalls[0].SessionID != expectedCall.SessionID ||
		failoverManager.failoverCalls[0].Reason != expectedCall.Reason {
		t.Errorf("Expected failover call %+v, got %+v", expectedCall, failoverManager.failoverCalls[0])
	}
}

func TestKwikError_Creation(t *testing.T) {
	cause := fmt.Errorf("underlying error")
	kwikError := NewKwikError(utils.ErrTimeout, "Test timeout", cause)

	if kwikError.Code != utils.ErrTimeout {
		t.Errorf("Expected code %s, got %s", utils.ErrTimeout, kwikError.Code)
	}

	if kwikError.Message != "Test timeout" {
		t.Errorf("Expected message 'Test timeout', got %s", kwikError.Message)
	}

	if kwikError.Cause != cause {
		t.Errorf("Expected cause to be set")
	}

	if kwikError.Type != ErrorTypeTimeout {
		t.Errorf("Expected type %v, got %v", ErrorTypeTimeout, kwikError.Type)
	}

	if !kwikError.Recoverable {
		t.Errorf("Expected error to be recoverable")
	}
}

func TestKwikError_WithContext(t *testing.T) {
	kwikError := NewKwikError(utils.ErrTimeout, "Test error", nil)
	kwikError.WithContext("session_1", "path_1", 123, "test_component")

	if kwikError.SessionID != "session_1" {
		t.Errorf("Expected session ID 'session_1', got %s", kwikError.SessionID)
	}

	if kwikError.PathID != "path_1" {
		t.Errorf("Expected path ID 'path_1', got %s", kwikError.PathID)
	}

	if kwikError.StreamID != 123 {
		t.Errorf("Expected stream ID 123, got %d", kwikError.StreamID)
	}

	if kwikError.Component != "test_component" {
		t.Errorf("Expected component 'test_component', got %s", kwikError.Component)
	}
}

func TestKwikError_WithMetadata(t *testing.T) {
	kwikError := NewKwikError(utils.ErrTimeout, "Test error", nil)
	kwikError.WithMetadata("key1", "value1")
	kwikError.WithMetadata("key2", 42)

	if kwikError.Metadata["key1"] != "value1" {
		t.Errorf("Expected metadata key1 to be 'value1', got %v", kwikError.Metadata["key1"])
	}

	if kwikError.Metadata["key2"] != 42 {
		t.Errorf("Expected metadata key2 to be 42, got %v", kwikError.Metadata["key2"])
	}
}

func TestRetryWithBackoffStrategy(t *testing.T) {
	strategy := NewRetryWithBackoffStrategy(3, 100*time.Millisecond, 2.0)

	// Test CanRecover
	recoverableError := NewKwikError(utils.ErrTimeout, "Recoverable error", nil)
	if !strategy.CanRecover(recoverableError) {
		t.Errorf("Expected strategy to handle recoverable error")
	}

	// Test non-recoverable error
	nonRecoverableError := NewKwikError(utils.ErrAuthenticationFailed, "Auth failed", nil)
	if strategy.CanRecover(nonRecoverableError) {
		t.Errorf("Expected strategy to reject non-recoverable error")
	}

	// Test max retries
	recoverableError.RetryCount = 5
	if strategy.CanRecover(recoverableError) {
		t.Errorf("Expected strategy to reject error with too many retries")
	}

	// Test recovery
	recoverableError.RetryCount = 1
	ctx := context.Background()
	recoveryContext := &RecoveryContext{
		Error:         recoverableError,
		AttemptNumber: 2,
		StartTime:     time.Now(),
	}

	result, err := strategy.Recover(ctx, recoveryContext)
	if err != nil {
		t.Fatalf("Recovery failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected successful recovery")
	}

	if result.Strategy != StrategyTypeRetryWithBackoff {
		t.Errorf("Expected retry strategy type")
	}
}

func TestPathFailoverStrategy(t *testing.T) {
	failoverManager := &MockFailoverManager{}
	strategy := NewPathFailoverStrategy(failoverManager)

	// Test CanRecover
	pathError := NewKwikError(utils.ErrPathDead, "Path dead", nil)
	if !strategy.CanRecover(pathError) {
		t.Errorf("Expected strategy to handle path error")
	}

	networkError := NewKwikError(utils.ErrConnectionLost, "Connection lost", nil)
	if !strategy.CanRecover(networkError) {
		t.Errorf("Expected strategy to handle network error")
	}

	authError := NewKwikError(utils.ErrAuthenticationFailed, "Auth failed", nil)
	if strategy.CanRecover(authError) {
		t.Errorf("Expected strategy to reject auth error")
	}

	// Test recovery
	ctx := context.Background()
	recoveryContext := &RecoveryContext{
		Error:     pathError,
		SessionID: "session_1",
		PathID:    "path_1",
		StartTime: time.Now(),
	}

	result, err := strategy.Recover(ctx, recoveryContext)
	if err != nil {
		t.Fatalf("Recovery failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected successful recovery")
	}

	if result.Strategy != StrategyTypePathFailover {
		t.Errorf("Expected path failover strategy type")
	}

	// Check that failover was called
	if len(failoverManager.failoverCalls) != 1 {
		t.Errorf("Expected 1 failover call, got %d", len(failoverManager.failoverCalls))
	}
}

func TestConnectionResetStrategy(t *testing.T) {
	strategy := NewConnectionResetStrategy()

	// Test CanRecover
	protocolError := NewKwikError(utils.ErrInvalidFrame, "Invalid frame", nil)
	if !strategy.CanRecover(protocolError) {
		t.Errorf("Expected strategy to handle protocol error")
	}

	sessionError := NewKwikError(utils.ErrSessionClosed, "Session closed", nil)
	if !strategy.CanRecover(sessionError) {
		t.Errorf("Expected strategy to handle session error")
	}

	pathError := NewKwikError(utils.ErrPathDead, "Path dead", nil)
	if strategy.CanRecover(pathError) {
		t.Errorf("Expected strategy to reject path error")
	}

	// Test recovery
	ctx := context.Background()
	recoveryContext := &RecoveryContext{
		Error:     protocolError,
		SessionID: "session_1",
		StartTime: time.Now(),
	}

	result, err := strategy.Recover(ctx, recoveryContext)
	if err != nil {
		t.Fatalf("Recovery failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected successful recovery")
	}

	if result.Strategy != StrategyTypeConnectionReset {
		t.Errorf("Expected connection reset strategy type")
	}
}

func TestErrorClassification(t *testing.T) {
	tests := []struct {
		code           string
		expectedType   ErrorType
		expectedSeverity ErrorSeverity
		expectedCategory ErrorCategory
		expectedRecoverable bool
	}{
		{utils.ErrTimeout, ErrorTypeTimeout, SeverityMedium, CategoryTransient, true},
		{utils.ErrConnectionLost, ErrorTypeNetwork, SeverityHigh, CategoryTransient, true},
		{utils.ErrAuthenticationFailed, ErrorTypeAuthentication, SeverityCritical, CategorySecurity, false},
		{utils.ErrPathDead, ErrorTypePath, SeverityLow, CategoryPermanent, true},
		{utils.ErrResourceExhausted, ErrorTypeResource, SeverityMedium, CategoryResource, true},
		{utils.ErrConfigurationInvalid, ErrorTypeConfiguration, SeverityLow, CategoryConfiguration, true},
	}

	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			errorType := classifyErrorType(test.code)
			if errorType != test.expectedType {
				t.Errorf("Expected type %v, got %v", test.expectedType, errorType)
			}

			severity := classifyErrorSeverity(test.code)
			if severity != test.expectedSeverity {
				t.Errorf("Expected severity %v, got %v", test.expectedSeverity, severity)
			}

			category := classifyErrorCategory(test.code)
			if category != test.expectedCategory {
				t.Errorf("Expected category %v, got %v", test.expectedCategory, category)
			}

			recoverable := isRecoverable(test.code)
			if recoverable != test.expectedRecoverable {
				t.Errorf("Expected recoverable %v, got %v", test.expectedRecoverable, recoverable)
			}
		})
	}
}

func TestRecoveryConfig(t *testing.T) {
	config := DefaultRecoveryConfig()

	if config.MaxConcurrentRecoveries <= 0 {
		t.Errorf("Expected positive max concurrent recoveries")
	}

	if config.DefaultMaxRetries <= 0 {
		t.Errorf("Expected positive default max retries")
	}

	if config.DefaultRetryDelay <= 0 {
		t.Errorf("Expected positive default retry delay")
	}

	if config.DefaultBackoffMultiplier <= 1.0 {
		t.Errorf("Expected backoff multiplier > 1.0")
	}
}

// Benchmark tests
func BenchmarkErrorRecovery(b *testing.B) {
	logger := &MockSessionLogger{}
	healthMonitor := &MockHealthMonitor{}
	failoverManager := &MockFailoverManager{}
	
	ers := NewErrorRecoverySystem(logger, healthMonitor, failoverManager, nil)
	defer ers.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kwikError := NewKwikError(utils.ErrTimeout, "Benchmark error", nil)
		kwikError.WithContext(fmt.Sprintf("session_%d", i), "path_1", uint64(i), "benchmark")

		_, err := ers.RecoverFromError(ctx, kwikError)
		if err != nil {
			b.Fatalf("Recovery failed: %v", err)
		}
	}
}