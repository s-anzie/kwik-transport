package data

import (
	"math"
	"math/rand"
	"sync"
	"time"
	
	"kwik/internal/utils"
)

// ExponentialBackoffStrategy implements exponential backoff with jitter
type ExponentialBackoffStrategy struct {
	InitialDelay  time.Duration `json:"initial_delay"`
	MaxDelay      time.Duration `json:"max_delay"`
	Multiplier    float64       `json:"multiplier"`
	MaxRetries    int           `json:"max_retries"`
	JitterEnabled bool          `json:"jitter_enabled"`
	JitterFactor  float64       `json:"jitter_factor"`
}

// NewExponentialBackoffStrategy creates a new exponential backoff strategy with default values
func NewExponentialBackoffStrategy() *ExponentialBackoffStrategy {
	return &ExponentialBackoffStrategy{
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      30 * time.Second,
		Multiplier:    2.0,
		MaxRetries:    5,
		JitterEnabled: true,
		JitterFactor:  0.1,
	}
}

// NewCustomExponentialBackoffStrategy creates a new exponential backoff strategy with custom values
func NewCustomExponentialBackoffStrategy(initialDelay, maxDelay time.Duration, multiplier float64, maxAttempts int) *ExponentialBackoffStrategy {
	return &ExponentialBackoffStrategy{
		InitialDelay:  initialDelay,
		MaxDelay:      maxDelay,
		Multiplier:    multiplier,
		MaxRetries:    maxAttempts,
		JitterEnabled: true,
		JitterFactor:  0.1,
	}
}

// NextDelay implements BackoffStrategy.NextDelay
func (ebs *ExponentialBackoffStrategy) NextDelay(attempt int, baseRTT time.Duration) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	
	// Calculate exponential delay
	delay := ebs.InitialDelay
	for i := 1; i < attempt; i++ {
		delay = time.Duration(float64(delay) * ebs.Multiplier)
		if delay > ebs.MaxDelay {
			delay = ebs.MaxDelay
			break
		}
	}
	
	// Factor in base RTT (use the larger of calculated delay or 2*RTT)
	if baseRTT > 0 {
		minDelay := time.Duration(float64(baseRTT) * 2.0)
		if delay < minDelay {
			delay = minDelay
		}
	}
	
	// Apply jitter to avoid thundering herd
	if ebs.JitterEnabled {
		jitter := ebs.calculateJitter(delay)
		delay = delay + jitter
	}
	
	// Ensure we don't exceed max delay
	if delay > ebs.MaxDelay {
		delay = ebs.MaxDelay
	}
	
	return delay
}

// MaxAttempts implements BackoffStrategy.MaxAttempts
func (ebs *ExponentialBackoffStrategy) MaxAttempts() int {
	return ebs.MaxRetries
}

// ShouldRetry implements BackoffStrategy.ShouldRetry
func (ebs *ExponentialBackoffStrategy) ShouldRetry(attempt int, lastError error) bool {
	if attempt > ebs.MaxRetries {
		return false
	}
	
	// Check if the error is retryable
	if lastError != nil {
		if kwikErr, ok := lastError.(*utils.KwikError); ok {
			return kwikErr.IsRetryable()
		}
	}
	
	return true
}

// SetJitter enables or disables jitter
func (ebs *ExponentialBackoffStrategy) SetJitter(enabled bool, factor float64) {
	ebs.JitterEnabled = enabled
	if factor > 0 && factor < 1.0 {
		ebs.JitterFactor = factor
	}
}

// calculateJitter calculates jitter based on the delay and jitter factor
func (ebs *ExponentialBackoffStrategy) calculateJitter(delay time.Duration) time.Duration {
	if !ebs.JitterEnabled || ebs.JitterFactor <= 0 {
		return 0
	}
	
	// Calculate jitter as a random value between -factor*delay and +factor*delay
	maxJitter := float64(delay) * ebs.JitterFactor
	jitter := (rand.Float64()*2 - 1) * maxJitter // Random value between -maxJitter and +maxJitter
	
	return time.Duration(jitter)
}

// LinearBackoffStrategy implements linear backoff
type LinearBackoffStrategy struct {
	InitialDelay time.Duration `json:"initial_delay"`
	Increment    time.Duration `json:"increment"`
	MaxDelay     time.Duration `json:"max_delay"`
	MaxRetries   int           `json:"max_retries"`
}

// NewLinearBackoffStrategy creates a new linear backoff strategy
func NewLinearBackoffStrategy(initialDelay, increment, maxDelay time.Duration, maxAttempts int) *LinearBackoffStrategy {
	return &LinearBackoffStrategy{
		InitialDelay: initialDelay,
		Increment:    increment,
		MaxDelay:     maxDelay,
		MaxRetries:   maxAttempts,
	}
}

// NextDelay implements BackoffStrategy.NextDelay
func (lbs *LinearBackoffStrategy) NextDelay(attempt int, baseRTT time.Duration) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	
	delay := lbs.InitialDelay + time.Duration(attempt-1)*lbs.Increment
	
	// Factor in base RTT
	if baseRTT > 0 {
		minDelay := time.Duration(float64(baseRTT) * 1.5)
		if delay < minDelay {
			delay = minDelay
		}
	}
	
	if delay > lbs.MaxDelay {
		delay = lbs.MaxDelay
	}
	
	return delay
}

// MaxAttempts implements BackoffStrategy.MaxAttempts
func (lbs *LinearBackoffStrategy) MaxAttempts() int {
	return lbs.MaxRetries
}

// ShouldRetry implements BackoffStrategy.ShouldRetry
func (lbs *LinearBackoffStrategy) ShouldRetry(attempt int, lastError error) bool {
	return attempt <= lbs.MaxRetries
}

// FixedBackoffStrategy implements fixed delay backoff
type FixedBackoffStrategy struct {
	Delay      time.Duration `json:"delay"`
	MaxRetries int           `json:"max_retries"`
}

// NewFixedBackoffStrategy creates a new fixed backoff strategy
func NewFixedBackoffStrategy(delay time.Duration, maxAttempts int) *FixedBackoffStrategy {
	return &FixedBackoffStrategy{
		Delay:      delay,
		MaxRetries: maxAttempts,
	}
}

// NextDelay implements BackoffStrategy.NextDelay
func (fbs *FixedBackoffStrategy) NextDelay(attempt int, baseRTT time.Duration) time.Duration {
	delay := fbs.Delay
	
	// Factor in base RTT
	if baseRTT > 0 {
		minDelay := time.Duration(float64(baseRTT) * 1.2)
		if delay < minDelay {
			delay = minDelay
		}
	}
	
	return delay
}

// MaxAttempts implements BackoffStrategy.MaxAttempts
func (fbs *FixedBackoffStrategy) MaxAttempts() int {
	return fbs.MaxRetries
}

// ShouldRetry implements BackoffStrategy.ShouldRetry
func (fbs *FixedBackoffStrategy) ShouldRetry(attempt int, lastError error) bool {
	return attempt <= fbs.MaxRetries
}

// AdaptiveBackoffStrategy implements RTT-based adaptive backoff
type AdaptiveBackoffStrategy struct {
	MinDelay      time.Duration `json:"min_delay"`
	MaxDelay      time.Duration `json:"max_delay"`
	RTTMultiplier float64       `json:"rtt_multiplier"`
	MaxRetries    int           `json:"max_retries"`
	
	// RTT tracking
	rttHistory   []time.Duration
	rttHistoryMu sync.RWMutex
	maxHistory   int
}

// NewAdaptiveBackoffStrategy creates a new adaptive backoff strategy
func NewAdaptiveBackoffStrategy() *AdaptiveBackoffStrategy {
	return &AdaptiveBackoffStrategy{
		MinDelay:      50 * time.Millisecond,
		MaxDelay:      10 * time.Second,
		RTTMultiplier: 3.0,
		MaxRetries:    4,
		maxHistory:    10,
		rttHistory:    make([]time.Duration, 0, 10),
	}
}

// NextDelay implements BackoffStrategy.NextDelay
func (abs *AdaptiveBackoffStrategy) NextDelay(attempt int, baseRTT time.Duration) time.Duration {
	// Update RTT history
	if baseRTT > 0 {
		abs.updateRTTHistory(baseRTT)
	}
	
	// Calculate delay based on smoothed RTT
	smoothedRTT := abs.getSmoothedRTT()
	if smoothedRTT == 0 {
		smoothedRTT = 100 * time.Millisecond // Default if no RTT data
	}
	
	// Exponential backoff based on attempt number
	multiplier := math.Pow(2.0, float64(attempt-1))
	delay := time.Duration(float64(smoothedRTT) * abs.RTTMultiplier * multiplier)
	
	// Apply bounds
	if delay < abs.MinDelay {
		delay = abs.MinDelay
	}
	if delay > abs.MaxDelay {
		delay = abs.MaxDelay
	}
	
	return delay
}

// MaxAttempts implements BackoffStrategy.MaxAttempts
func (abs *AdaptiveBackoffStrategy) MaxAttempts() int {
	return abs.MaxRetries
}

// ShouldRetry implements BackoffStrategy.ShouldRetry
func (abs *AdaptiveBackoffStrategy) ShouldRetry(attempt int, lastError error) bool {
	return attempt <= abs.MaxRetries
}

// updateRTTHistory updates the RTT history for smoothing
func (abs *AdaptiveBackoffStrategy) updateRTTHistory(rtt time.Duration) {
	abs.rttHistoryMu.Lock()
	defer abs.rttHistoryMu.Unlock()
	
	abs.rttHistory = append(abs.rttHistory, rtt)
	if len(abs.rttHistory) > abs.maxHistory {
		abs.rttHistory = abs.rttHistory[1:]
	}
}

// getSmoothedRTT calculates smoothed RTT from history
func (abs *AdaptiveBackoffStrategy) getSmoothedRTT() time.Duration {
	abs.rttHistoryMu.RLock()
	defer abs.rttHistoryMu.RUnlock()
	
	if len(abs.rttHistory) == 0 {
		return 0
	}
	
	// Calculate exponentially weighted moving average
	var smoothed float64
	alpha := 0.125 // Standard TCP smoothing factor
	
	smoothed = float64(abs.rttHistory[0])
	for i := 1; i < len(abs.rttHistory); i++ {
		smoothed = (1-alpha)*smoothed + alpha*float64(abs.rttHistory[i])
	}
	
	return time.Duration(smoothed)
}