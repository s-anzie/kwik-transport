package session

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"kwik/internal/utils"
)

// ErrorClassifier provides advanced error classification and analysis
type ErrorClassifier struct {
	// Classification rules
	rules []ClassificationRule
	mutex sync.RWMutex

	// Pattern matchers for error messages
	patterns map[ErrorType][]*regexp.Regexp

	// Context analyzers
	contextAnalyzers []ContextAnalyzer

	// Historical data for pattern learning
	errorHistory []ClassifiedError
	historyMutex sync.RWMutex

	// Configuration
	config ClassifierConfig
}

// ClassificationRule defines how to classify an error
type ClassificationRule struct {
	Name        string                    `json:"name"`
	Priority    int                       `json:"priority"` // Higher priority rules are checked first
	Condition   func(error) bool         `json:"-"`
	ErrorType   ErrorType                `json:"error_type"`
	Severity    ErrorSeverity            `json:"severity"`
	Category    ErrorCategory            `json:"category"`
	Recoverable bool                     `json:"recoverable"`
	Confidence  float64                  `json:"confidence"` // 0.0 to 1.0
}

// ContextAnalyzer analyzes error context to improve classification
type ContextAnalyzer interface {
	AnalyzeContext(ctx context.Context, err error, metadata map[string]interface{}) *ContextAnalysis
	GetAnalyzerType() ContextAnalyzerType
}

// ContextAnalyzerType defines the type of context analyzer
type ContextAnalyzerType int

const (
	ContextAnalyzerTypeNetwork ContextAnalyzerType = iota
	ContextAnalyzerTypeSession
	ContextAnalyzerTypeStream
	ContextAnalyzerTypeProtocol
	ContextAnalyzerTypeResource
)

// ContextAnalysis contains the result of context analysis
type ContextAnalysis struct {
	AnalyzerType ContextAnalyzerType      `json:"analyzer_type"`
	Confidence   float64                  `json:"confidence"`
	Factors      []ContextFactor          `json:"factors"`
	Suggestions  []ClassificationSuggestion `json:"suggestions"`
}

// ContextFactor represents a factor that influences error classification
type ContextFactor struct {
	Name        string      `json:"name"`
	Value       interface{} `json:"value"`
	Weight      float64     `json:"weight"`
	Impact      string      `json:"impact"`
}

// ClassificationSuggestion suggests how to classify an error
type ClassificationSuggestion struct {
	ErrorType   ErrorType     `json:"error_type"`
	Severity    ErrorSeverity `json:"severity"`
	Category    ErrorCategory `json:"category"`
	Confidence  float64       `json:"confidence"`
	Reasoning   string        `json:"reasoning"`
}

// ClassifiedError represents an error that has been classified
type ClassifiedError struct {
	OriginalError error                    `json:"-"`
	ErrorCode     string                   `json:"error_code"`
	Message       string                   `json:"message"`
	Classification ErrorClassification     `json:"classification"`
	Context       map[string]interface{}   `json:"context"`
	Timestamp     time.Time                `json:"timestamp"`
	SessionID     string                   `json:"session_id"`
	PathID        string                   `json:"path_id"`
	StreamID      uint64                   `json:"stream_id"`
}

// ErrorClassification contains the complete classification of an error
type ErrorClassification struct {
	Type        ErrorType     `json:"type"`
	Severity    ErrorSeverity `json:"severity"`
	Category    ErrorCategory `json:"category"`
	Recoverable bool          `json:"recoverable"`
	Confidence  float64       `json:"confidence"`
	Rules       []string      `json:"rules"`        // Names of rules that matched
	Patterns    []string      `json:"patterns"`     // Patterns that matched
	Context     []ContextAnalysis `json:"context"` // Context analysis results
}

// ClassifierConfig contains configuration for the error classifier
type ClassifierConfig struct {
	// Pattern matching
	EnablePatternMatching bool `json:"enable_pattern_matching"`
	CaseSensitivePatterns bool `json:"case_sensitive_patterns"`

	// Context analysis
	EnableContextAnalysis bool `json:"enable_context_analysis"`
	ContextTimeout        time.Duration `json:"context_timeout"`

	// Historical learning
	EnableHistoricalLearning bool `json:"enable_historical_learning"`
	HistorySize              int  `json:"history_size"`
	LearningThreshold        int  `json:"learning_threshold"`

	// Classification thresholds
	MinConfidenceThreshold float64 `json:"min_confidence_threshold"`
	DefaultConfidence      float64 `json:"default_confidence"`
}

// DefaultClassifierConfig returns default configuration for the error classifier
func DefaultClassifierConfig() ClassifierConfig {
	return ClassifierConfig{
		EnablePatternMatching:    true,
		CaseSensitivePatterns:    false,
		EnableContextAnalysis:    true,
		ContextTimeout:           1 * time.Second,
		EnableHistoricalLearning: true,
		HistorySize:              1000,
		LearningThreshold:        10,
		MinConfidenceThreshold:   0.5,
		DefaultConfidence:        0.7,
	}
}

// NewErrorClassifier creates a new error classifier with default rules
func NewErrorClassifier(config ClassifierConfig) *ErrorClassifier {
	ec := &ErrorClassifier{
		rules:            make([]ClassificationRule, 0),
		patterns:         make(map[ErrorType][]*regexp.Regexp),
		contextAnalyzers: make([]ContextAnalyzer, 0),
		errorHistory:     make([]ClassifiedError, 0, config.HistorySize),
		config:           config,
	}

	// Add default classification rules
	ec.addDefaultRules()

	// Add default patterns
	ec.addDefaultPatterns()

	// Add default context analyzers
	ec.addDefaultContextAnalyzers()

	return ec
}

// ClassifyError classifies an error with full analysis
func (ec *ErrorClassifier) ClassifyError(ctx context.Context, err error, metadata map[string]interface{}) *ErrorClassification {
	if err == nil {
		return nil
	}

	classification := &ErrorClassification{
		Type:        ErrorTypeUnknown,
		Severity:    SeverityLow,
		Category:    CategoryPermanent,
		Recoverable: false,
		Confidence:  0.0,
		Rules:       make([]string, 0),
		Patterns:    make([]string, 0),
		Context:     make([]ContextAnalysis, 0),
	}

	// Apply classification rules
	ec.applyRules(err, classification)

	// Apply pattern matching
	if ec.config.EnablePatternMatching {
		ec.applyPatterns(err, classification)
	}

	// Apply context analysis
	if ec.config.EnableContextAnalysis {
		ec.applyContextAnalysis(ctx, err, metadata, classification)
	}

	// Apply historical learning
	if ec.config.EnableHistoricalLearning {
		ec.applyHistoricalLearning(err, classification)
	}

	// Ensure minimum confidence
	if classification.Confidence < ec.config.MinConfidenceThreshold {
		classification.Confidence = ec.config.DefaultConfidence
	}

	// Record classified error for learning
	ec.recordClassifiedError(err, classification, metadata)

	return classification
}

// AddRule adds a custom classification rule
func (ec *ErrorClassifier) AddRule(rule ClassificationRule) {
	ec.mutex.Lock()
	defer ec.mutex.Unlock()

	// Insert rule in priority order
	inserted := false
	for i, existingRule := range ec.rules {
		if rule.Priority > existingRule.Priority {
			// Insert at position i
			ec.rules = append(ec.rules[:i], append([]ClassificationRule{rule}, ec.rules[i:]...)...)
			inserted = true
			break
		}
	}

	if !inserted {
		ec.rules = append(ec.rules, rule)
	}
}

// AddPattern adds a pattern for a specific error type
func (ec *ErrorClassifier) AddPattern(errorType ErrorType, pattern string) error {
	ec.mutex.Lock()
	defer ec.mutex.Unlock()

	// Add case insensitive flag if needed
	if !ec.config.CaseSensitivePatterns {
		pattern = "(?i)" + pattern
	}

	regex, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern: %v", err)
	}

	if ec.patterns[errorType] == nil {
		ec.patterns[errorType] = make([]*regexp.Regexp, 0)
	}

	ec.patterns[errorType] = append(ec.patterns[errorType], regex)
	return nil
}

// AddContextAnalyzer adds a context analyzer
func (ec *ErrorClassifier) AddContextAnalyzer(analyzer ContextAnalyzer) {
	ec.mutex.Lock()
	defer ec.mutex.Unlock()
	ec.contextAnalyzers = append(ec.contextAnalyzers, analyzer)
}

// GetClassificationStats returns statistics about error classifications
func (ec *ErrorClassifier) GetClassificationStats() ClassificationStats {
	ec.historyMutex.RLock()
	defer ec.historyMutex.RUnlock()

	stats := ClassificationStats{
		TotalClassifications: len(ec.errorHistory),
		TypeDistribution:     make(map[ErrorType]int),
		SeverityDistribution: make(map[ErrorSeverity]int),
		CategoryDistribution: make(map[ErrorCategory]int),
		AverageConfidence:    0.0,
	}

	if len(ec.errorHistory) == 0 {
		return stats
	}

	totalConfidence := 0.0
	for _, classifiedError := range ec.errorHistory {
		stats.TypeDistribution[classifiedError.Classification.Type]++
		stats.SeverityDistribution[classifiedError.Classification.Severity]++
		stats.CategoryDistribution[classifiedError.Classification.Category]++
		totalConfidence += classifiedError.Classification.Confidence
	}

	stats.AverageConfidence = totalConfidence / float64(len(ec.errorHistory))
	return stats
}

// ClassificationStats contains statistics about error classifications
type ClassificationStats struct {
	TotalClassifications int                        `json:"total_classifications"`
	TypeDistribution     map[ErrorType]int          `json:"type_distribution"`
	SeverityDistribution map[ErrorSeverity]int      `json:"severity_distribution"`
	CategoryDistribution map[ErrorCategory]int      `json:"category_distribution"`
	AverageConfidence    float64                    `json:"average_confidence"`
}

// applyRules applies classification rules to an error
func (ec *ErrorClassifier) applyRules(err error, classification *ErrorClassification) {
	ec.mutex.RLock()
	defer ec.mutex.RUnlock()

	bestConfidence := 0.0
	for _, rule := range ec.rules {
		if rule.Condition(err) {
			if rule.Confidence > bestConfidence {
				classification.Type = rule.ErrorType
				classification.Severity = rule.Severity
				classification.Category = rule.Category
				classification.Recoverable = rule.Recoverable
				classification.Confidence = rule.Confidence
				bestConfidence = rule.Confidence
			}
			classification.Rules = append(classification.Rules, rule.Name)
		}
	}
}

// applyPatterns applies pattern matching to an error
func (ec *ErrorClassifier) applyPatterns(err error, classification *ErrorClassification) {
	ec.mutex.RLock()
	defer ec.mutex.RUnlock()

	errorMessage := err.Error()
	for errorType, patterns := range ec.patterns {
		for _, pattern := range patterns {
			if pattern.MatchString(errorMessage) {
				// Pattern matched, increase confidence if it matches current classification
				if classification.Type == errorType {
					classification.Confidence = min(1.0, classification.Confidence+0.2)
				} else if classification.Confidence < 0.6 {
					// Override if current confidence is low
					classification.Type = errorType
					classification.Confidence = 0.6
				}
				classification.Patterns = append(classification.Patterns, pattern.String())
			}
		}
	}
}

// applyContextAnalysis applies context analysis to an error
func (ec *ErrorClassifier) applyContextAnalysis(ctx context.Context, err error, metadata map[string]interface{}, classification *ErrorClassification) {
	ec.mutex.RLock()
	analyzers := make([]ContextAnalyzer, len(ec.contextAnalyzers))
	copy(analyzers, ec.contextAnalyzers)
	ec.mutex.RUnlock()

	// Create context with timeout for analysis
	analysisCtx, cancel := context.WithTimeout(ctx, ec.config.ContextTimeout)
	defer cancel()

	for _, analyzer := range analyzers {
		analysis := analyzer.AnalyzeContext(analysisCtx, err, metadata)
		if analysis != nil {
			classification.Context = append(classification.Context, *analysis)

			// Apply suggestions from context analysis
			for _, suggestion := range analysis.Suggestions {
				if suggestion.Confidence > classification.Confidence {
					classification.Type = suggestion.ErrorType
					classification.Severity = suggestion.Severity
					classification.Category = suggestion.Category
					classification.Confidence = suggestion.Confidence
				}
			}
		}
	}
}

// applyHistoricalLearning applies historical learning to improve classification
func (ec *ErrorClassifier) applyHistoricalLearning(err error, classification *ErrorClassification) {
	ec.historyMutex.RLock()
	defer ec.historyMutex.RUnlock()

	errorMessage := err.Error()
	similarErrors := 0
	matchingClassifications := make(map[ErrorType]int)

	// Look for similar errors in history
	for _, historicalError := range ec.errorHistory {
		if ec.isSimilarError(errorMessage, historicalError.Message) {
			similarErrors++
			matchingClassifications[historicalError.Classification.Type]++
		}
	}

	// If we have enough similar errors, use the most common classification
	if similarErrors >= ec.config.LearningThreshold {
		mostCommonType := ErrorTypeUnknown
		maxCount := 0
		for errorType, count := range matchingClassifications {
			if count > maxCount {
				mostCommonType = errorType
				maxCount = count
			}
		}

		if mostCommonType != ErrorTypeUnknown {
			confidence := float64(maxCount) / float64(similarErrors)
			if confidence > classification.Confidence {
				classification.Type = mostCommonType
				classification.Confidence = confidence
			}
		}
	}
}

// recordClassifiedError records a classified error for learning
func (ec *ErrorClassifier) recordClassifiedError(err error, classification *ErrorClassification, metadata map[string]interface{}) {
	ec.historyMutex.Lock()
	defer ec.historyMutex.Unlock()

	classifiedError := ClassifiedError{
		OriginalError:  err,
		ErrorCode:      getErrorCode(err),
		Message:        err.Error(),
		Classification: *classification,
		Context:        metadata,
		Timestamp:      time.Now(),
	}

	// Extract session/path/stream info from metadata if available
	if sessionID, ok := metadata["session_id"].(string); ok {
		classifiedError.SessionID = sessionID
	}
	if pathID, ok := metadata["path_id"].(string); ok {
		classifiedError.PathID = pathID
	}
	if streamID, ok := metadata["stream_id"].(uint64); ok {
		classifiedError.StreamID = streamID
	}

	// Add to history
	ec.errorHistory = append(ec.errorHistory, classifiedError)

	// Trim history if it exceeds the configured size
	if len(ec.errorHistory) > ec.config.HistorySize {
		ec.errorHistory = ec.errorHistory[1:]
	}
}

// isSimilarError determines if two error messages are similar
func (ec *ErrorClassifier) isSimilarError(msg1, msg2 string) bool {
	// Simple similarity check - can be enhanced with more sophisticated algorithms
	msg1 = strings.ToLower(strings.TrimSpace(msg1))
	msg2 = strings.ToLower(strings.TrimSpace(msg2))

	// Exact match
	if msg1 == msg2 {
		return true
	}

	// Check if one contains the other (for partial matches)
	if len(msg1) > 10 && len(msg2) > 10 {
		return strings.Contains(msg1, msg2) || strings.Contains(msg2, msg1)
	}

	return false
}

// getErrorCode extracts error code from an error
func getErrorCode(err error) string {
	// Try to extract KWIK error code
	if kwikErr, ok := err.(*KwikError); ok {
		return kwikErr.Code
	}

	// Try to extract from error message
	errorMsg := err.Error()
	if strings.Contains(errorMsg, "KWIK_") {
		parts := strings.Fields(errorMsg)
		for _, part := range parts {
			if strings.HasPrefix(part, "KWIK_") {
				return strings.TrimSuffix(part, ":")
			}
		}
	}

	return "UNKNOWN"
}

// addDefaultRules adds default classification rules
func (ec *ErrorClassifier) addDefaultRules() {
	// Network-related errors
	ec.AddRule(ClassificationRule{
		Name:     "Connection Timeout",
		Priority: 100,
		Condition: func(err error) bool {
			msg := strings.ToLower(err.Error())
			return strings.Contains(msg, "timeout") && 
				   (strings.Contains(msg, "connection") || strings.Contains(msg, "network"))
		},
		ErrorType:   ErrorTypeNetwork,
		Severity:    SeverityHigh,
		Category:    CategoryTransient,
		Recoverable: true,
		Confidence:  0.9,
	})

	// Protocol errors
	ec.AddRule(ClassificationRule{
		Name:     "Invalid Frame",
		Priority: 95,
		Condition: func(err error) bool {
			return strings.Contains(err.Error(), utils.ErrInvalidFrame)
		},
		ErrorType:   ErrorTypeProtocol,
		Severity:    SeverityMedium,
		Category:    CategoryPermanent,
		Recoverable: true,
		Confidence:  0.95,
	})

	// Authentication errors
	ec.AddRule(ClassificationRule{
		Name:     "Authentication Failed",
		Priority: 90,
		Condition: func(err error) bool {
			return strings.Contains(err.Error(), utils.ErrAuthenticationFailed)
		},
		ErrorType:   ErrorTypeAuthentication,
		Severity:    SeverityCritical,
		Category:    CategorySecurity,
		Recoverable: false,
		Confidence:  0.98,
	})

	// Resource errors
	ec.AddRule(ClassificationRule{
		Name:     "Resource Exhausted",
		Priority: 85,
		Condition: func(err error) bool {
			return strings.Contains(err.Error(), utils.ErrResourceExhausted)
		},
		ErrorType:   ErrorTypeResource,
		Severity:    SeverityHigh,
		Category:    CategoryResource,
		Recoverable: true,
		Confidence:  0.9,
	})

	// Path errors
	ec.AddRule(ClassificationRule{
		Name:     "Path Dead",
		Priority: 80,
		Condition: func(err error) bool {
			return strings.Contains(err.Error(), utils.ErrPathDead)
		},
		ErrorType:   ErrorTypePath,
		Severity:    SeverityHigh,
		Category:    CategoryTransient,
		Recoverable: true,
		Confidence:  0.9,
	})

	// Session errors
	ec.AddRule(ClassificationRule{
		Name:     "Session Not Found",
		Priority: 75,
		Condition: func(err error) bool {
			return strings.Contains(err.Error(), utils.ErrSessionNotFound)
		},
		ErrorType:   ErrorTypeSession,
		Severity:    SeverityMedium,
		Category:    CategoryPermanent,
		Recoverable: true,
		Confidence:  0.85,
	})

	// Stream errors
	ec.AddRule(ClassificationRule{
		Name:     "Stream Creation Failed",
		Priority: 70,
		Condition: func(err error) bool {
			return strings.Contains(err.Error(), utils.ErrStreamCreationFailed)
		},
		ErrorType:   ErrorTypeStream,
		Severity:    SeverityMedium,
		Category:    CategoryTransient,
		Recoverable: true,
		Confidence:  0.8,
	})
}

// addDefaultPatterns adds default error patterns
func (ec *ErrorClassifier) addDefaultPatterns() {
	// Network patterns
	ec.AddPattern(ErrorTypeNetwork, `connection.*refused`)
	ec.AddPattern(ErrorTypeNetwork, `network.*unreachable`)
	ec.AddPattern(ErrorTypeNetwork, `no route to host`)
	ec.AddPattern(ErrorTypeNetwork, `connection.*reset`)

	// Timeout patterns
	ec.AddPattern(ErrorTypeTimeout, `timeout.*exceeded`)
	ec.AddPattern(ErrorTypeTimeout, `operation.*timed out`)
	ec.AddPattern(ErrorTypeTimeout, `deadline.*exceeded`)

	// Protocol patterns
	ec.AddPattern(ErrorTypeProtocol, `invalid.*frame`)
	ec.AddPattern(ErrorTypeProtocol, `protocol.*violation`)
	ec.AddPattern(ErrorTypeProtocol, `malformed.*packet`)

	// Resource patterns
	ec.AddPattern(ErrorTypeResource, `out of memory`)
	ec.AddPattern(ErrorTypeResource, `resource.*exhausted`)
	ec.AddPattern(ErrorTypeResource, `buffer.*full`)
}

// addDefaultContextAnalyzers adds default context analyzers
func (ec *ErrorClassifier) addDefaultContextAnalyzers() {
	// Network context analyzer
	ec.AddContextAnalyzer(&NetworkContextAnalyzer{})
	
	// Session context analyzer
	ec.AddContextAnalyzer(&SessionContextAnalyzer{})
	
	// Resource context analyzer
	ec.AddContextAnalyzer(&ResourceContextAnalyzer{})
}

// Helper function for min
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
// NetworkContextAnalyzer analyzes network-related context
type NetworkContextAnalyzer struct{}

func (nca *NetworkContextAnalyzer) AnalyzeContext(ctx context.Context, err error, metadata map[string]interface{}) *ContextAnalysis {
	analysis := &ContextAnalysis{
		AnalyzerType: ContextAnalyzerTypeNetwork,
		Confidence:   0.0,
		Factors:      make([]ContextFactor, 0),
		Suggestions:  make([]ClassificationSuggestion, 0),
	}

	errorMsg := strings.ToLower(err.Error())
	
	// Check for network-related keywords
	networkKeywords := []string{"connection", "network", "socket", "tcp", "udp", "dns", "timeout"}
	networkScore := 0.0
	for _, keyword := range networkKeywords {
		if strings.Contains(errorMsg, keyword) {
			networkScore += 0.2
			analysis.Factors = append(analysis.Factors, ContextFactor{
				Name:   "NetworkKeyword",
				Value:  keyword,
				Weight: 0.2,
				Impact: "Indicates network-related error",
			})
		}
	}

	// Check path information from metadata
	if pathID, ok := metadata["path_id"].(string); ok && pathID != "" {
		analysis.Factors = append(analysis.Factors, ContextFactor{
			Name:   "PathContext",
			Value:  pathID,
			Weight: 0.3,
			Impact: "Error occurred in specific path context",
		})
		networkScore += 0.3
	}

	// Check for connection state information
	if connState, ok := metadata["connection_state"].(string); ok {
		analysis.Factors = append(analysis.Factors, ContextFactor{
			Name:   "ConnectionState",
			Value:  connState,
			Weight: 0.4,
			Impact: "Connection state provides context for error classification",
		})
		if connState == "disconnected" || connState == "failed" {
			networkScore += 0.4
		}
	}

	analysis.Confidence = min(1.0, networkScore)

	// Make suggestions based on analysis
	if analysis.Confidence > 0.6 {
		analysis.Suggestions = append(analysis.Suggestions, ClassificationSuggestion{
			ErrorType:  ErrorTypeNetwork,
			Severity:   SeverityHigh,
			Category:   CategoryTransient,
			Confidence: analysis.Confidence,
			Reasoning:  "Network-related context factors detected",
		})
	}

	return analysis
}

func (nca *NetworkContextAnalyzer) GetAnalyzerType() ContextAnalyzerType {
	return ContextAnalyzerTypeNetwork
}

// SessionContextAnalyzer analyzes session-related context
type SessionContextAnalyzer struct{}

func (sca *SessionContextAnalyzer) AnalyzeContext(ctx context.Context, err error, metadata map[string]interface{}) *ContextAnalysis {
	analysis := &ContextAnalysis{
		AnalyzerType: ContextAnalyzerTypeSession,
		Confidence:   0.0,
		Factors:      make([]ContextFactor, 0),
		Suggestions:  make([]ClassificationSuggestion, 0),
	}

	errorMsg := strings.ToLower(err.Error())
	
	// Check for session-related keywords
	sessionKeywords := []string{"session", "handshake", "authentication", "authorization"}
	sessionScore := 0.0
	for _, keyword := range sessionKeywords {
		if strings.Contains(errorMsg, keyword) {
			sessionScore += 0.25
			analysis.Factors = append(analysis.Factors, ContextFactor{
				Name:   "SessionKeyword",
				Value:  keyword,
				Weight: 0.25,
				Impact: "Indicates session-related error",
			})
		}
	}

	// Check session information from metadata
	if sessionID, ok := metadata["session_id"].(string); ok && sessionID != "" {
		analysis.Factors = append(analysis.Factors, ContextFactor{
			Name:   "SessionContext",
			Value:  sessionID,
			Weight: 0.2,
			Impact: "Error occurred in specific session context",
		})
		sessionScore += 0.2
	}

	// Check session state
	if sessionState, ok := metadata["session_state"].(string); ok {
		analysis.Factors = append(analysis.Factors, ContextFactor{
			Name:   "SessionState",
			Value:  sessionState,
			Weight: 0.3,
			Impact: "Session state provides context for error classification",
		})
		if sessionState == "closed" || sessionState == "failed" {
			sessionScore += 0.3
		}
	}

	analysis.Confidence = min(1.0, sessionScore)

	// Make suggestions based on analysis
	if analysis.Confidence > 0.5 {
		errorType := ErrorTypeSession
		severity := SeverityMedium
		
		// Adjust based on specific keywords
		if strings.Contains(errorMsg, "authentication") || strings.Contains(errorMsg, "authorization") {
			errorType = ErrorTypeAuthentication
			severity = SeverityCritical
		}

		analysis.Suggestions = append(analysis.Suggestions, ClassificationSuggestion{
			ErrorType:  errorType,
			Severity:   severity,
			Category:   CategoryPermanent,
			Confidence: analysis.Confidence,
			Reasoning:  "Session-related context factors detected",
		})
	}

	return analysis
}

func (sca *SessionContextAnalyzer) GetAnalyzerType() ContextAnalyzerType {
	return ContextAnalyzerTypeSession
}

// ResourceContextAnalyzer analyzes resource-related context
type ResourceContextAnalyzer struct{}

func (rca *ResourceContextAnalyzer) AnalyzeContext(ctx context.Context, err error, metadata map[string]interface{}) *ContextAnalysis {
	analysis := &ContextAnalysis{
		AnalyzerType: ContextAnalyzerTypeResource,
		Confidence:   0.0,
		Factors:      make([]ContextFactor, 0),
		Suggestions:  make([]ClassificationSuggestion, 0),
	}

	errorMsg := strings.ToLower(err.Error())
	
	// Check for resource-related keywords
	resourceKeywords := []string{"memory", "buffer", "resource", "exhausted", "limit", "quota"}
	resourceScore := 0.0
	for _, keyword := range resourceKeywords {
		if strings.Contains(errorMsg, keyword) {
			resourceScore += 0.2
			analysis.Factors = append(analysis.Factors, ContextFactor{
				Name:   "ResourceKeyword",
				Value:  keyword,
				Weight: 0.2,
				Impact: "Indicates resource-related error",
			})
		}
	}

	// Check memory usage from metadata
	if memUsage, ok := metadata["memory_usage"].(float64); ok {
		analysis.Factors = append(analysis.Factors, ContextFactor{
			Name:   "MemoryUsage",
			Value:  memUsage,
			Weight: 0.3,
			Impact: "Memory usage level affects error classification",
		})
		if memUsage > 0.9 { // High memory usage
			resourceScore += 0.4
		} else if memUsage > 0.7 {
			resourceScore += 0.2
		}
	}

	// Check buffer usage from metadata
	if bufferUsage, ok := metadata["buffer_usage"].(float64); ok {
		analysis.Factors = append(analysis.Factors, ContextFactor{
			Name:   "BufferUsage",
			Value:  bufferUsage,
			Weight: 0.3,
			Impact: "Buffer usage level affects error classification",
		})
		if bufferUsage > 0.95 { // Very high buffer usage
			resourceScore += 0.4
		} else if bufferUsage > 0.8 {
			resourceScore += 0.2
		}
	}

	// Check connection count from metadata
	if connCount, ok := metadata["connection_count"].(int); ok {
		analysis.Factors = append(analysis.Factors, ContextFactor{
			Name:   "ConnectionCount",
			Value:  connCount,
			Weight: 0.2,
			Impact: "High connection count may indicate resource pressure",
		})
		if connCount > 1000 { // High connection count
			resourceScore += 0.3
		}
	}

	analysis.Confidence = min(1.0, resourceScore)

	// Make suggestions based on analysis
	if analysis.Confidence > 0.6 {
		analysis.Suggestions = append(analysis.Suggestions, ClassificationSuggestion{
			ErrorType:  ErrorTypeResource,
			Severity:   SeverityHigh,
			Category:   CategoryResource,
			Confidence: analysis.Confidence,
			Reasoning:  "Resource-related context factors detected",
		})
	}

	return analysis
}

func (rca *ResourceContextAnalyzer) GetAnalyzerType() ContextAnalyzerType {
	return ContextAnalyzerTypeResource
}

// EnhancedErrorClassifier extends the basic error classifier with machine learning capabilities
type EnhancedErrorClassifier struct {
	*ErrorClassifier
	
	// Machine learning components
	featureExtractor *FeatureExtractor
	patternLearner   *PatternLearner
	
	// Advanced configuration
	mlConfig MLConfig
}

// MLConfig contains machine learning configuration
type MLConfig struct {
	EnableFeatureExtraction bool          `json:"enable_feature_extraction"`
	EnablePatternLearning   bool          `json:"enable_pattern_learning"`
	LearningRate           float64       `json:"learning_rate"`
	MinSamplesForLearning  int           `json:"min_samples_for_learning"`
	FeatureUpdateInterval  time.Duration `json:"feature_update_interval"`
}

// FeatureExtractor extracts features from errors for machine learning
type FeatureExtractor struct {
	features map[string]float64
	mutex    sync.RWMutex
}

// PatternLearner learns patterns from historical errors
type PatternLearner struct {
	patterns map[string]PatternInfo
	mutex    sync.RWMutex
}

// PatternInfo contains information about learned patterns
type PatternInfo struct {
	Pattern     string    `json:"pattern"`
	Frequency   int       `json:"frequency"`
	ErrorType   ErrorType `json:"error_type"`
	Confidence  float64   `json:"confidence"`
	LastSeen    time.Time `json:"last_seen"`
}

// NewEnhancedErrorClassifier creates a new enhanced error classifier
func NewEnhancedErrorClassifier(config ClassifierConfig, mlConfig MLConfig) *EnhancedErrorClassifier {
	baseClassifier := NewErrorClassifier(config)
	
	return &EnhancedErrorClassifier{
		ErrorClassifier:  baseClassifier,
		featureExtractor: &FeatureExtractor{
			features: make(map[string]float64),
		},
		patternLearner: &PatternLearner{
			patterns: make(map[string]PatternInfo),
		},
		mlConfig: mlConfig,
	}
}

// ClassifyErrorEnhanced performs enhanced error classification with ML
func (eec *EnhancedErrorClassifier) ClassifyErrorEnhanced(ctx context.Context, err error, metadata map[string]interface{}) *ErrorClassification {
	// Start with basic classification
	classification := eec.ClassifyError(ctx, err, metadata)
	
	// Apply feature extraction if enabled
	if eec.mlConfig.EnableFeatureExtraction {
		features := eec.featureExtractor.ExtractFeatures(err, metadata)
		eec.applyFeatureBasedClassification(features, classification)
	}
	
	// Apply pattern learning if enabled
	if eec.mlConfig.EnablePatternLearning {
		eec.patternLearner.LearnFromError(err, classification)
		eec.applyLearnedPatterns(err, classification)
	}
	
	return classification
}

// ExtractFeatures extracts numerical features from an error
func (fe *FeatureExtractor) ExtractFeatures(err error, metadata map[string]interface{}) map[string]float64 {
	fe.mutex.Lock()
	defer fe.mutex.Unlock()
	
	features := make(map[string]float64)
	
	errorMsg := err.Error()
	
	// Text-based features
	features["message_length"] = float64(len(errorMsg))
	features["word_count"] = float64(len(strings.Fields(errorMsg)))
	features["uppercase_ratio"] = calculateUppercaseRatio(errorMsg)
	features["digit_ratio"] = calculateDigitRatio(errorMsg)
	
	// Metadata-based features
	if sessionID, ok := metadata["session_id"].(string); ok {
		features["has_session_id"] = 1.0
		features["session_id_length"] = float64(len(sessionID))
	}
	
	if pathID, ok := metadata["path_id"].(string); ok {
		features["has_path_id"] = 1.0
		features["path_id_length"] = float64(len(pathID))
	}
	
	if streamID, ok := metadata["stream_id"].(uint64); ok {
		features["has_stream_id"] = 1.0
		features["stream_id_value"] = float64(streamID)
	}
	
	// Time-based features
	if timestamp, ok := metadata["timestamp"].(time.Time); ok {
		features["hour_of_day"] = float64(timestamp.Hour())
		features["day_of_week"] = float64(timestamp.Weekday())
	}
	
	return features
}

// LearnFromError learns patterns from an error and its classification
func (pl *PatternLearner) LearnFromError(err error, classification *ErrorClassification) {
	pl.mutex.Lock()
	defer pl.mutex.Unlock()
	
	errorMsg := err.Error()
	
	// Extract key phrases from error message
	phrases := extractKeyPhrases(errorMsg)
	
	for _, phrase := range phrases {
		if info, exists := pl.patterns[phrase]; exists {
			// Update existing pattern
			info.Frequency++
			info.LastSeen = time.Now()
			// Update confidence based on consistency
			if info.ErrorType == classification.Type {
				info.Confidence = min(1.0, info.Confidence+0.1)
			} else {
				info.Confidence = max(0.0, info.Confidence-0.05)
			}
			pl.patterns[phrase] = info
		} else {
			// Create new pattern
			pl.patterns[phrase] = PatternInfo{
				Pattern:    phrase,
				Frequency:  1,
				ErrorType:  classification.Type,
				Confidence: 0.5,
				LastSeen:   time.Now(),
			}
		}
	}
}

// applyFeatureBasedClassification applies feature-based classification
func (eec *EnhancedErrorClassifier) applyFeatureBasedClassification(features map[string]float64, classification *ErrorClassification) {
	// Simple rule-based feature classification
	// In a real implementation, this would use a trained ML model
	
	if msgLen, ok := features["message_length"]; ok {
		if msgLen > 200 { // Very long error messages often indicate complex issues
			if classification.Severity < SeverityHigh {
				classification.Severity = SeverityHigh
				classification.Confidence = min(1.0, classification.Confidence+0.1)
			}
		}
	}
	
	if uppercaseRatio, ok := features["uppercase_ratio"]; ok {
		if uppercaseRatio > 0.5 { // High uppercase ratio might indicate system errors
			if classification.Type == ErrorTypeUnknown {
				classification.Type = ErrorTypeProtocol
				classification.Confidence = 0.6
			}
		}
	}
}

// applyLearnedPatterns applies learned patterns to classification
func (eec *EnhancedErrorClassifier) applyLearnedPatterns(err error, classification *ErrorClassification) {
	eec.patternLearner.mutex.RLock()
	defer eec.patternLearner.mutex.RUnlock()
	
	errorMsg := err.Error()
	phrases := extractKeyPhrases(errorMsg)
	
	bestConfidence := classification.Confidence
	bestType := classification.Type
	
	for _, phrase := range phrases {
		if info, exists := eec.patternLearner.patterns[phrase]; exists {
			if info.Confidence > bestConfidence && info.Frequency >= eec.mlConfig.MinSamplesForLearning {
				bestConfidence = info.Confidence
				bestType = info.ErrorType
			}
		}
	}
	
	if bestConfidence > classification.Confidence {
		classification.Type = bestType
		classification.Confidence = bestConfidence
	}
}

// Helper functions

func calculateUppercaseRatio(text string) float64 {
	if len(text) == 0 {
		return 0.0
	}
	
	uppercaseCount := 0
	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			uppercaseCount++
		}
	}
	
	return float64(uppercaseCount) / float64(len(text))
}

func calculateDigitRatio(text string) float64 {
	if len(text) == 0 {
		return 0.0
	}
	
	digitCount := 0
	for _, r := range text {
		if r >= '0' && r <= '9' {
			digitCount++
		}
	}
	
	return float64(digitCount) / float64(len(text))
}

func extractKeyPhrases(text string) []string {
	// Simple key phrase extraction
	words := strings.Fields(strings.ToLower(text))
	phrases := make([]string, 0)
	
	// Extract individual significant words
	for _, word := range words {
		if len(word) > 3 && !isCommonWord(word) {
			phrases = append(phrases, word)
		}
	}
	
	// Extract 2-word phrases
	for i := 0; i < len(words)-1; i++ {
		if len(words[i]) > 2 && len(words[i+1]) > 2 {
			phrase := words[i] + " " + words[i+1]
			phrases = append(phrases, phrase)
		}
	}
	
	return phrases
}

func isCommonWord(word string) bool {
	commonWords := map[string]bool{
		"the": true, "and": true, "or": true, "but": true, "in": true,
		"on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "is": true, "are": true, "was": true,
		"were": true, "be": true, "been": true, "have": true, "has": true,
		"had": true, "do": true, "does": true, "did": true, "will": true,
		"would": true, "could": true, "should": true, "may": true, "might": true,
	}
	
	return commonWords[word]
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}