package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"kwik/internal/utils"
)

func TestErrorClassifier_BasicClassification(t *testing.T) {
	config := DefaultClassifierConfig()
	classifier := NewErrorClassifier(config)

	tests := []struct {
		name           string
		error          error
		expectedType   ErrorType
		expectedSeverity ErrorSeverity
		minConfidence  float64
	}{
		{
			name:           "Authentication Error",
			error:          fmt.Errorf(utils.ErrAuthenticationFailed + ": invalid credentials"),
			expectedType:   ErrorTypeAuthentication,
			expectedSeverity: SeverityCritical,
			minConfidence:  0.9,
		},
		{
			name:           "Network Timeout",
			error:          fmt.Errorf("connection timeout: network unreachable"),
			expectedType:   ErrorTypeNetwork,
			expectedSeverity: SeverityHigh,
			minConfidence:  0.8,
		},
		{
			name:           "Resource Exhausted",
			error:          fmt.Errorf(utils.ErrResourceExhausted + ": memory limit exceeded"),
			expectedType:   ErrorTypeResource,
			expectedSeverity: SeverityHigh,
			minConfidence:  0.8,
		},
		{
			name:           "Path Dead",
			error:          fmt.Errorf(utils.ErrPathDead + ": path connection lost"),
			expectedType:   ErrorTypePath,
			expectedSeverity: SeverityHigh,
			minConfidence:  0.8,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			metadata := make(map[string]interface{})

			classification := classifier.ClassifyError(ctx, test.error, metadata)

			if classification == nil {
				t.Fatalf("Expected classification, got nil")
			}

			if classification.Type != test.expectedType {
				t.Errorf("Expected type %v, got %v", test.expectedType, classification.Type)
			}

			if classification.Severity != test.expectedSeverity {
				t.Errorf("Expected severity %v, got %v", test.expectedSeverity, classification.Severity)
			}

			if classification.Confidence < test.minConfidence {
				t.Errorf("Expected confidence >= %f, got %f", test.minConfidence, classification.Confidence)
			}
		})
	}
}

func TestErrorClassifier_PatternMatching(t *testing.T) {
	config := DefaultClassifierConfig()
	classifier := NewErrorClassifier(config)

	// Add custom pattern
	err := classifier.AddPattern(ErrorTypeNetwork, `connection.*failed`)
	if err != nil {
		t.Fatalf("Failed to add pattern: %v", err)
	}

	ctx := context.Background()
	metadata := make(map[string]interface{})

	// Test error that should match the pattern
	testError := fmt.Errorf("connection to server failed")
	classification := classifier.ClassifyError(ctx, testError, metadata)

	if classification.Type != ErrorTypeNetwork {
		t.Errorf("Expected type %v, got %v", ErrorTypeNetwork, classification.Type)
	}

	if len(classification.Patterns) == 0 {
		t.Errorf("Expected patterns to be matched, got none")
	}
}

func TestErrorClassifier_ContextAnalysis(t *testing.T) {
	config := DefaultClassifierConfig()
	classifier := NewErrorClassifier(config)

	ctx := context.Background()
	metadata := map[string]interface{}{
		"session_id":       "test_session_123",
		"path_id":          "path_456",
		"connection_state": "failed",
		"memory_usage":     0.95, // High memory usage
	}

	testError := fmt.Errorf("network connection failed")
	classification := classifier.ClassifyError(ctx, testError, metadata)

	if classification == nil {
		t.Fatalf("Expected classification, got nil")
	}

	// Should have context analysis results
	if len(classification.Context) == 0 {
		t.Errorf("Expected context analysis results, got none")
	}

	// Check that network context analyzer was applied
	foundNetworkAnalysis := false
	for _, analysis := range classification.Context {
		if analysis.AnalyzerType == ContextAnalyzerTypeNetwork {
			foundNetworkAnalysis = true
			if analysis.Confidence == 0 {
				t.Errorf("Expected network analysis confidence > 0, got %f", analysis.Confidence)
			}
		}
	}

	if !foundNetworkAnalysis {
		t.Errorf("Expected network context analysis, but not found")
	}
}

func TestErrorClassifier_CustomRules(t *testing.T) {
	config := DefaultClassifierConfig()
	classifier := NewErrorClassifier(config)

	// Add custom rule
	customRule := ClassificationRule{
		Name:     "Custom Test Rule",
		Priority: 200, // High priority
		Condition: func(err error) bool {
			return err.Error() == "custom test error"
		},
		ErrorType:   ErrorTypeData,
		Severity:    SeverityMedium,
		Category:    CategoryPermanent,
		Recoverable: false,
		Confidence:  0.99,
	}

	classifier.AddRule(customRule)

	ctx := context.Background()
	metadata := make(map[string]interface{})

	testError := fmt.Errorf("custom test error")
	classification := classifier.ClassifyError(ctx, testError, metadata)

	if classification.Type != ErrorTypeData {
		t.Errorf("Expected type %v, got %v", ErrorTypeData, classification.Type)
	}

	if classification.Severity != SeverityMedium {
		t.Errorf("Expected severity %v, got %v", SeverityMedium, classification.Severity)
	}

	if classification.Confidence != 0.99 {
		t.Errorf("Expected confidence 0.99, got %f", classification.Confidence)
	}

	// Check that the custom rule was applied
	foundCustomRule := false
	for _, ruleName := range classification.Rules {
		if ruleName == "Custom Test Rule" {
			foundCustomRule = true
			break
		}
	}

	if !foundCustomRule {
		t.Errorf("Expected custom rule to be applied, but not found in rules: %v", classification.Rules)
	}
}

func TestErrorClassifier_HistoricalLearning(t *testing.T) {
	config := DefaultClassifierConfig()
	config.EnableHistoricalLearning = true
	config.LearningThreshold = 2 // Low threshold for testing
	classifier := NewErrorClassifier(config)

	ctx := context.Background()
	metadata := make(map[string]interface{})

	// Classify the same error multiple times to build history
	testError := fmt.Errorf("repeated test error for learning")
	
	for i := 0; i < 3; i++ {
		classification := classifier.ClassifyError(ctx, testError, metadata)
		if classification == nil {
			t.Fatalf("Expected classification, got nil")
		}
	}

	// The fourth classification should benefit from historical learning
	finalClassification := classifier.ClassifyError(ctx, testError, metadata)
	
	if finalClassification == nil {
		t.Fatalf("Expected final classification, got nil")
	}

	// Check that we have some history
	stats := classifier.GetClassificationStats()
	if stats.TotalClassifications < 3 {
		t.Errorf("Expected at least 3 classifications in history, got %d", stats.TotalClassifications)
	}
}

func TestErrorClassifier_Stats(t *testing.T) {
	config := DefaultClassifierConfig()
	classifier := NewErrorClassifier(config)

	ctx := context.Background()
	metadata := make(map[string]interface{})

	// Classify various errors
	errors := []error{
		fmt.Errorf(utils.ErrAuthenticationFailed + ": test"),
		fmt.Errorf(utils.ErrResourceExhausted + ": test"),
		fmt.Errorf(utils.ErrPathDead + ": test"),
		fmt.Errorf(utils.ErrAuthenticationFailed + ": test2"),
	}

	for _, err := range errors {
		classifier.ClassifyError(ctx, err, metadata)
	}

	stats := classifier.GetClassificationStats()

	if stats.TotalClassifications != len(errors) {
		t.Errorf("Expected %d total classifications, got %d", len(errors), stats.TotalClassifications)
	}

	if stats.TypeDistribution[ErrorTypeAuthentication] != 2 {
		t.Errorf("Expected 2 authentication errors, got %d", stats.TypeDistribution[ErrorTypeAuthentication])
	}

	if stats.AverageConfidence == 0 {
		t.Errorf("Expected average confidence > 0, got %f", stats.AverageConfidence)
	}
}

func TestNetworkContextAnalyzer(t *testing.T) {
	analyzer := &NetworkContextAnalyzer{}

	ctx := context.Background()
	testError := fmt.Errorf("connection timeout: network unreachable")
	metadata := map[string]interface{}{
		"path_id":          "test_path",
		"connection_state": "failed",
	}

	analysis := analyzer.AnalyzeContext(ctx, testError, metadata)

	if analysis == nil {
		t.Fatalf("Expected analysis, got nil")
	}

	if analysis.AnalyzerType != ContextAnalyzerTypeNetwork {
		t.Errorf("Expected network analyzer type, got %v", analysis.AnalyzerType)
	}

	if analysis.Confidence == 0 {
		t.Errorf("Expected confidence > 0, got %f", analysis.Confidence)
	}

	if len(analysis.Factors) == 0 {
		t.Errorf("Expected context factors, got none")
	}

	if len(analysis.Suggestions) == 0 {
		t.Errorf("Expected suggestions, got none")
	}
}

func TestSessionContextAnalyzer(t *testing.T) {
	analyzer := &SessionContextAnalyzer{}

	ctx := context.Background()
	testError := fmt.Errorf("session authentication failed")
	metadata := map[string]interface{}{
		"session_id":    "test_session",
		"session_state": "failed",
	}

	analysis := analyzer.AnalyzeContext(ctx, testError, metadata)

	if analysis == nil {
		t.Fatalf("Expected analysis, got nil")
	}

	if analysis.AnalyzerType != ContextAnalyzerTypeSession {
		t.Errorf("Expected session analyzer type, got %v", analysis.AnalyzerType)
	}

	if analysis.Confidence == 0 {
		t.Errorf("Expected confidence > 0, got %f", analysis.Confidence)
	}

	// Should suggest authentication error type for authentication-related errors
	foundAuthSuggestion := false
	for _, suggestion := range analysis.Suggestions {
		if suggestion.ErrorType == ErrorTypeAuthentication {
			foundAuthSuggestion = true
			break
		}
	}

	if !foundAuthSuggestion {
		t.Errorf("Expected authentication error suggestion for auth-related error")
	}
}

func TestResourceContextAnalyzer(t *testing.T) {
	analyzer := &ResourceContextAnalyzer{}

	ctx := context.Background()
	testError := fmt.Errorf("buffer exhausted: memory limit exceeded")
	metadata := map[string]interface{}{
		"memory_usage":     0.95,
		"buffer_usage":     0.98,
		"connection_count": 1500,
	}

	analysis := analyzer.AnalyzeContext(ctx, testError, metadata)

	if analysis == nil {
		t.Fatalf("Expected analysis, got nil")
	}

	if analysis.AnalyzerType != ContextAnalyzerTypeResource {
		t.Errorf("Expected resource analyzer type, got %v", analysis.AnalyzerType)
	}

	if analysis.Confidence < 0.6 {
		t.Errorf("Expected high confidence for resource error with high usage, got %f", analysis.Confidence)
	}

	// Should have multiple factors due to high resource usage
	if len(analysis.Factors) < 3 {
		t.Errorf("Expected multiple resource factors, got %d", len(analysis.Factors))
	}
}

func TestEnhancedErrorClassifier(t *testing.T) {
	config := DefaultClassifierConfig()
	mlConfig := MLConfig{
		EnableFeatureExtraction: true,
		EnablePatternLearning:   true,
		LearningRate:           0.1,
		MinSamplesForLearning:  2,
		FeatureUpdateInterval:  1 * time.Minute,
	}

	classifier := NewEnhancedErrorClassifier(config, mlConfig)

	ctx := context.Background()
	metadata := map[string]interface{}{
		"session_id": "test_session",
		"timestamp":  time.Now(),
	}

	testError := fmt.Errorf("NETWORK CONNECTION FAILED WITH TIMEOUT")
	classification := classifier.ClassifyErrorEnhanced(ctx, testError, metadata)

	if classification == nil {
		t.Fatalf("Expected classification, got nil")
	}

	// Enhanced classifier should provide classification
	if classification.Type == ErrorTypeUnknown {
		t.Errorf("Enhanced classifier should classify the error, got Unknown type")
	}

	if classification.Confidence == 0 {
		t.Errorf("Expected confidence > 0, got %f", classification.Confidence)
	}
}

func TestFeatureExtractor(t *testing.T) {
	extractor := &FeatureExtractor{
		features: make(map[string]float64),
	}

	testError := fmt.Errorf("TEST ERROR MESSAGE WITH 123 NUMBERS")
	metadata := map[string]interface{}{
		"session_id": "test_session_123",
		"path_id":    "path_456",
		"stream_id":  uint64(789),
		"timestamp":  time.Now(),
	}

	features := extractor.ExtractFeatures(testError, metadata)

	// Check that basic features are extracted
	if _, ok := features["message_length"]; !ok {
		t.Errorf("Expected message_length feature")
	}

	if _, ok := features["word_count"]; !ok {
		t.Errorf("Expected word_count feature")
	}

	if _, ok := features["uppercase_ratio"]; !ok {
		t.Errorf("Expected uppercase_ratio feature")
	}

	if _, ok := features["digit_ratio"]; !ok {
		t.Errorf("Expected digit_ratio feature")
	}

	// Check metadata-based features
	if features["has_session_id"] != 1.0 {
		t.Errorf("Expected has_session_id to be 1.0, got %f", features["has_session_id"])
	}

	if features["has_path_id"] != 1.0 {
		t.Errorf("Expected has_path_id to be 1.0, got %f", features["has_path_id"])
	}

	if features["has_stream_id"] != 1.0 {
		t.Errorf("Expected has_stream_id to be 1.0, got %f", features["has_stream_id"])
	}
}

func TestPatternLearner(t *testing.T) {
	learner := &PatternLearner{
		patterns: make(map[string]PatternInfo),
	}

	testError := fmt.Errorf("connection timeout error")
	classification := &ErrorClassification{
		Type:       ErrorTypeNetwork,
		Confidence: 0.8,
	}

	// Learn from the error multiple times
	for i := 0; i < 3; i++ {
		learner.LearnFromError(testError, classification)
	}

	// Check that patterns were learned
	if len(learner.patterns) == 0 {
		t.Errorf("Expected learned patterns, got none")
	}

	// Check that pattern frequency increased
	foundPattern := false
	for _, info := range learner.patterns {
		if info.Frequency >= 3 && info.ErrorType == ErrorTypeNetwork {
			foundPattern = true
			break
		}
	}

	if !foundPattern {
		t.Errorf("Expected to find learned pattern with correct frequency and type")
	}
}

func TestHelperFunctions(t *testing.T) {
	// Test calculateUppercaseRatio
	ratio := calculateUppercaseRatio("HELLO world")
	expected := 5.0 / 11.0 // 5 uppercase letters out of 11 total
	if ratio != expected {
		t.Errorf("Expected uppercase ratio %f, got %f", expected, ratio)
	}

	// Test calculateDigitRatio
	ratio = calculateDigitRatio("abc123def")
	expected = 3.0 / 9.0 // 3 digits out of 9 total
	if ratio != expected {
		t.Errorf("Expected digit ratio %f, got %f", expected, ratio)
	}

	// Test extractKeyPhrases
	phrases := extractKeyPhrases("connection timeout error occurred")
	if len(phrases) == 0 {
		t.Errorf("Expected key phrases, got none")
	}

	// Should extract significant words
	foundConnection := false
	foundTimeout := false
	for _, phrase := range phrases {
		if phrase == "connection" {
			foundConnection = true
		}
		if phrase == "timeout" {
			foundTimeout = true
		}
	}

	if !foundConnection || !foundTimeout {
		t.Errorf("Expected to extract 'connection' and 'timeout' as key phrases, got: %v", phrases)
	}
}

// Benchmark tests
func BenchmarkErrorClassification(b *testing.B) {
	config := DefaultClassifierConfig()
	classifier := NewErrorClassifier(config)

	ctx := context.Background()
	metadata := make(map[string]interface{})
	testError := fmt.Errorf(utils.ErrConnectionLost + ": network timeout")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		classifier.ClassifyError(ctx, testError, metadata)
	}
}

func BenchmarkEnhancedErrorClassification(b *testing.B) {
	config := DefaultClassifierConfig()
	mlConfig := MLConfig{
		EnableFeatureExtraction: true,
		EnablePatternLearning:   true,
		LearningRate:           0.1,
		MinSamplesForLearning:  2,
	}

	classifier := NewEnhancedErrorClassifier(config, mlConfig)

	ctx := context.Background()
	metadata := map[string]interface{}{
		"session_id": "test_session",
		"timestamp":  time.Now(),
	}
	testError := fmt.Errorf("NETWORK CONNECTION FAILED")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		classifier.ClassifyErrorEnhanced(ctx, testError, metadata)
	}
}