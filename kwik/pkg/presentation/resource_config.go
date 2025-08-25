package presentation

import (
	"fmt"
	"sync"
	"time"
)

// ResourceManagementConfig contains comprehensive configuration for resource management
type ResourceManagementConfig struct {
	// Stream limits
	MaxConcurrentStreams    int     `json:"max_concurrent_streams"`    // Maximum concurrent streams
	MaxStreamsPerConnection int     `json:"max_streams_per_connection"` // Maximum streams per connection
	StreamCreationRate      float64 `json:"stream_creation_rate"`      // Maximum streams created per second
	
	// Memory-based allocation
	MemoryBasedAllocation   bool    `json:"memory_based_allocation"`   // Enable memory-based allocation
	MemoryThreshold         float64 `json:"memory_threshold"`          // Memory threshold for allocation (0.0-1.0)
	MemoryPressureThreshold float64 `json:"memory_pressure_threshold"` // Memory pressure threshold
	
	// Priority-based allocation
	PriorityBasedAllocation bool                        `json:"priority_based_allocation"` // Enable priority-based allocation
	PriorityWeights         map[StreamPriority]float64  `json:"priority_weights"`          // Weights for each priority level
	PriorityReservation     map[StreamPriority]float64  `json:"priority_reservation"`      // Reserved resources per priority
	
	// Buffer management
	DynamicBufferSizing     bool    `json:"dynamic_buffer_sizing"`     // Enable dynamic buffer sizing
	MinBufferSize           uint64  `json:"min_buffer_size"`           // Minimum buffer size per stream
	MaxBufferSize           uint64  `json:"max_buffer_size"`           // Maximum buffer size per stream
	BufferGrowthFactor      float64 `json:"buffer_growth_factor"`      // Buffer growth factor
	BufferShrinkThreshold   float64 `json:"buffer_shrink_threshold"`   // Buffer shrink threshold
	
	// Window management
	DynamicWindowSizing     bool    `json:"dynamic_window_sizing"`     // Enable dynamic window sizing
	WindowGrowthRate        float64 `json:"window_growth_rate"`        // Window growth rate
	WindowShrinkRate        float64 `json:"window_shrink_rate"`        // Window shrink rate
	WindowUtilizationTarget float64 `json:"window_utilization_target"` // Target window utilization
	
	// Cleanup and GC
	AggressiveCleanup       bool          `json:"aggressive_cleanup"`       // Enable aggressive cleanup
	CleanupInterval         time.Duration `json:"cleanup_interval"`         // Cleanup interval
	IdleStreamTimeout       time.Duration `json:"idle_stream_timeout"`      // Timeout for idle streams
	OrphanedResourceTimeout time.Duration `json:"orphaned_resource_timeout"` // Timeout for orphaned resources
	
	// Monitoring and alerting
	EnableResourceMonitoring bool          `json:"enable_resource_monitoring"` // Enable resource monitoring
	MonitoringInterval       time.Duration `json:"monitoring_interval"`        // Monitoring interval
	AlertThresholds          *ResourceThresholds `json:"alert_thresholds"`     // Alert thresholds
	
	// Performance tuning
	PreallocationEnabled    bool    `json:"preallocation_enabled"`    // Enable resource preallocation
	PreallocationSize       uint64  `json:"preallocation_size"`       // Size to preallocate
	LoadBalancingEnabled    bool    `json:"load_balancing_enabled"`   // Enable load balancing
	LoadBalancingStrategy   string  `json:"load_balancing_strategy"`  // Load balancing strategy
	
	// Adaptive behavior
	AdaptiveAllocation      bool    `json:"adaptive_allocation"`      // Enable adaptive allocation
	AdaptationInterval      time.Duration `json:"adaptation_interval"` // Adaptation interval
	LearningRate            float64 `json:"learning_rate"`            // Learning rate for adaptation
	
	// Limits and quotas
	GlobalMemoryLimit       uint64  `json:"global_memory_limit"`      // Global memory limit
	PerStreamMemoryLimit    uint64  `json:"per_stream_memory_limit"`  // Per-stream memory limit
	ConnectionMemoryLimit   uint64  `json:"connection_memory_limit"`  // Per-connection memory limit
	
	// Fairness and QoS
	FairnessEnabled         bool    `json:"fairness_enabled"`         // Enable fairness algorithms
	QoSEnabled              bool    `json:"qos_enabled"`              // Enable QoS management
	BandwidthAllocation     string  `json:"bandwidth_allocation"`     // Bandwidth allocation strategy
}

// DefaultResourceManagementConfig returns default resource management configuration
func DefaultResourceManagementConfig() *ResourceManagementConfig {
	return &ResourceManagementConfig{
		// Stream limits
		MaxConcurrentStreams:    1000,
		MaxStreamsPerConnection: 100,
		StreamCreationRate:      100.0, // 100 streams/second
		
		// Memory-based allocation
		MemoryBasedAllocation:   true,
		MemoryThreshold:         0.8,  // 80%
		MemoryPressureThreshold: 0.9,  // 90%
		
		// Priority-based allocation
		PriorityBasedAllocation: true,
		PriorityWeights: map[StreamPriority]float64{
			StreamPriorityLow:      0.1,
			StreamPriorityNormal:   0.5,
			StreamPriorityHigh:     0.8,
			StreamPriorityCritical: 1.0,
		},
		PriorityReservation: map[StreamPriority]float64{
			StreamPriorityLow:      0.1,  // 10% reserved
			StreamPriorityNormal:   0.4,  // 40% reserved
			StreamPriorityHigh:     0.3,  // 30% reserved
			StreamPriorityCritical: 0.2,  // 20% reserved
		},
		
		// Buffer management
		DynamicBufferSizing:   true,
		MinBufferSize:         4 * 1024,      // 4KB
		MaxBufferSize:         1024 * 1024,   // 1MB
		BufferGrowthFactor:    2.0,
		BufferShrinkThreshold: 0.25, // 25%
		
		// Window management
		DynamicWindowSizing:     true,
		WindowGrowthRate:        1.5,
		WindowShrinkRate:        0.8,
		WindowUtilizationTarget: 0.7, // 70%
		
		// Cleanup and GC
		AggressiveCleanup:       false,
		CleanupInterval:         30 * time.Second,
		IdleStreamTimeout:       5 * time.Minute,
		OrphanedResourceTimeout: 10 * time.Minute,
		
		// Monitoring and alerting
		EnableResourceMonitoring: true,
		MonitoringInterval:       10 * time.Second,
		AlertThresholds:          DefaultResourceThresholds(),
		
		// Performance tuning
		PreallocationEnabled:  false,
		PreallocationSize:     1024 * 1024, // 1MB
		LoadBalancingEnabled:  true,
		LoadBalancingStrategy: "round_robin",
		
		// Adaptive behavior
		AdaptiveAllocation: true,
		AdaptationInterval: 1 * time.Minute,
		LearningRate:       0.1,
		
		// Limits and quotas
		GlobalMemoryLimit:     100 * 1024 * 1024, // 100MB
		PerStreamMemoryLimit:  1024 * 1024,       // 1MB
		ConnectionMemoryLimit: 10 * 1024 * 1024,  // 10MB
		
		// Fairness and QoS
		FairnessEnabled:     true,
		QoSEnabled:          true,
		BandwidthAllocation: "weighted_fair_queuing",
	}
}

// Validate validates the resource management configuration
func (rmc *ResourceManagementConfig) Validate() error {
	if rmc.MaxConcurrentStreams <= 0 {
		return fmt.Errorf("max_concurrent_streams must be positive")
	}
	
	if rmc.MaxStreamsPerConnection <= 0 {
		return fmt.Errorf("max_streams_per_connection must be positive")
	}
	
	if rmc.StreamCreationRate <= 0 {
		return fmt.Errorf("stream_creation_rate must be positive")
	}
	
	if rmc.MemoryThreshold < 0 || rmc.MemoryThreshold > 1 {
		return fmt.Errorf("memory_threshold must be between 0 and 1")
	}
	
	if rmc.MemoryPressureThreshold < 0 || rmc.MemoryPressureThreshold > 1 {
		return fmt.Errorf("memory_pressure_threshold must be between 0 and 1")
	}
	
	if rmc.MemoryPressureThreshold < rmc.MemoryThreshold {
		return fmt.Errorf("memory_pressure_threshold must be >= memory_threshold")
	}
	
	if rmc.MinBufferSize == 0 {
		return fmt.Errorf("min_buffer_size cannot be zero")
	}
	
	if rmc.MaxBufferSize < rmc.MinBufferSize {
		return fmt.Errorf("max_buffer_size must be >= min_buffer_size")
	}
	
	if rmc.BufferGrowthFactor <= 1.0 {
		return fmt.Errorf("buffer_growth_factor must be > 1.0")
	}
	
	if rmc.BufferShrinkThreshold <= 0 || rmc.BufferShrinkThreshold >= 1 {
		return fmt.Errorf("buffer_shrink_threshold must be between 0 and 1")
	}
	
	if rmc.WindowGrowthRate <= 1.0 {
		return fmt.Errorf("window_growth_rate must be > 1.0")
	}
	
	if rmc.WindowShrinkRate <= 0 || rmc.WindowShrinkRate >= 1 {
		return fmt.Errorf("window_shrink_rate must be between 0 and 1")
	}
	
	if rmc.WindowUtilizationTarget <= 0 || rmc.WindowUtilizationTarget >= 1 {
		return fmt.Errorf("window_utilization_target must be between 0 and 1")
	}
	
	if rmc.CleanupInterval <= 0 {
		return fmt.Errorf("cleanup_interval must be positive")
	}
	
	if rmc.IdleStreamTimeout <= 0 {
		return fmt.Errorf("idle_stream_timeout must be positive")
	}
	
	if rmc.OrphanedResourceTimeout <= 0 {
		return fmt.Errorf("orphaned_resource_timeout must be positive")
	}
	
	if rmc.MonitoringInterval <= 0 {
		return fmt.Errorf("monitoring_interval must be positive")
	}
	
	if rmc.AdaptationInterval <= 0 {
		return fmt.Errorf("adaptation_interval must be positive")
	}
	
	if rmc.LearningRate <= 0 || rmc.LearningRate >= 1 {
		return fmt.Errorf("learning_rate must be between 0 and 1")
	}
	
	if rmc.GlobalMemoryLimit == 0 {
		return fmt.Errorf("global_memory_limit cannot be zero")
	}
	
	if rmc.PerStreamMemoryLimit == 0 {
		return fmt.Errorf("per_stream_memory_limit cannot be zero")
	}
	
	if rmc.ConnectionMemoryLimit == 0 {
		return fmt.Errorf("connection_memory_limit cannot be zero")
	}
	
	// Validate priority weights sum to reasonable value
	if rmc.PriorityBasedAllocation {
		totalWeight := 0.0
		for _, weight := range rmc.PriorityWeights {
			if weight < 0 {
				return fmt.Errorf("priority weights must be non-negative")
			}
			totalWeight += weight
		}
		if totalWeight == 0 {
			return fmt.Errorf("total priority weights cannot be zero")
		}
		
		// Validate priority reservations sum to <= 1.0
		totalReservation := 0.0
		for _, reservation := range rmc.PriorityReservation {
			if reservation < 0 || reservation > 1 {
				return fmt.Errorf("priority reservations must be between 0 and 1")
			}
			totalReservation += reservation
		}
		if totalReservation > 1.0 {
			return fmt.Errorf("total priority reservations cannot exceed 1.0")
		}
	}
	
	return nil
}

// ResourceAllocationStrategy defines different resource allocation strategies
type ResourceAllocationStrategy int

const (
	AllocationStrategyFirstFit ResourceAllocationStrategy = iota
	AllocationStrategyBestFit
	AllocationStrategyWorstFit
	AllocationStrategyRoundRobin
	AllocationStrategyWeightedFair
	AllocationStrategyPriorityBased
	AllocationStrategyAdaptive
)

func (ras ResourceAllocationStrategy) String() string {
	switch ras {
	case AllocationStrategyFirstFit:
		return "first_fit"
	case AllocationStrategyBestFit:
		return "best_fit"
	case AllocationStrategyWorstFit:
		return "worst_fit"
	case AllocationStrategyRoundRobin:
		return "round_robin"
	case AllocationStrategyWeightedFair:
		return "weighted_fair"
	case AllocationStrategyPriorityBased:
		return "priority_based"
	case AllocationStrategyAdaptive:
		return "adaptive"
	default:
		return "unknown"
	}
}

// ResourceQuota defines resource quotas for different entities
type ResourceQuota struct {
	// Memory quotas
	MemoryLimit     uint64 `json:"memory_limit"`     // Total memory limit
	MemoryReserved  uint64 `json:"memory_reserved"`  // Reserved memory
	MemoryUsed      uint64 `json:"memory_used"`      // Currently used memory
	
	// Stream quotas
	StreamLimit     int    `json:"stream_limit"`     // Maximum streams
	StreamReserved  int    `json:"stream_reserved"`  // Reserved streams
	StreamUsed      int    `json:"stream_used"`      // Currently used streams
	
	// Bandwidth quotas
	BandwidthLimit  uint64 `json:"bandwidth_limit"`  // Bandwidth limit (bytes/sec)
	BandwidthUsed   uint64 `json:"bandwidth_used"`   // Currently used bandwidth
	
	// Window quotas
	WindowLimit     uint64 `json:"window_limit"`     // Window space limit
	WindowReserved  uint64 `json:"window_reserved"`  // Reserved window space
	WindowUsed      uint64 `json:"window_used"`      // Currently used window space
}

// ResourcePolicy defines policies for resource management
type ResourcePolicy struct {
	// Allocation policies
	AllocationStrategy      ResourceAllocationStrategy `json:"allocation_strategy"`
	PreemptionEnabled       bool                      `json:"preemption_enabled"`
	PreemptionThreshold     float64                   `json:"preemption_threshold"`
	
	// Fairness policies
	FairShareEnabled        bool    `json:"fair_share_enabled"`
	MinimumGuarantee        float64 `json:"minimum_guarantee"`
	MaximumShare            float64 `json:"maximum_share"`
	
	// Priority policies
	PriorityPreemption      bool                      `json:"priority_preemption"`
	PriorityInversion       bool                      `json:"priority_inversion"`
	PriorityAging           bool                      `json:"priority_aging"`
	AgingFactor             float64                   `json:"aging_factor"`
	
	// Adaptive policies
	AdaptiveEnabled         bool    `json:"adaptive_enabled"`
	AdaptationThreshold     float64 `json:"adaptation_threshold"`
	AdaptationRate          float64 `json:"adaptation_rate"`
	
	// Cleanup policies
	AutoCleanupEnabled      bool          `json:"auto_cleanup_enabled"`
	CleanupThreshold        float64       `json:"cleanup_threshold"`
	GracePeriod             time.Duration `json:"grace_period"`
}

// DefaultResourcePolicy returns default resource policy
func DefaultResourcePolicy() *ResourcePolicy {
	return &ResourcePolicy{
		AllocationStrategy:   AllocationStrategyWeightedFair,
		PreemptionEnabled:    true,
		PreemptionThreshold:  0.9, // 90%
		
		FairShareEnabled:     true,
		MinimumGuarantee:     0.1, // 10%
		MaximumShare:         0.8, // 80%
		
		PriorityPreemption:   true,
		PriorityInversion:    false,
		PriorityAging:        true,
		AgingFactor:          0.01, // 1% per interval
		
		AdaptiveEnabled:      true,
		AdaptationThreshold:  0.8, // 80%
		AdaptationRate:       0.1, // 10%
		
		AutoCleanupEnabled:   true,
		CleanupThreshold:     0.9, // 90%
		GracePeriod:          30 * time.Second,
	}
}

// ResourceProfile defines different resource profiles for different use cases
type ResourceProfile struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Config      *ResourceManagementConfig  `json:"config"`
	Policy      *ResourcePolicy            `json:"policy"`
}

// PredefinedResourceProfiles returns a set of predefined resource profiles
func PredefinedResourceProfiles() map[string]*ResourceProfile {
	return map[string]*ResourceProfile{
		"low_latency": {
			Name:        "Low Latency",
			Description: "Optimized for low latency applications",
			Config: &ResourceManagementConfig{
				MaxConcurrentStreams:    500,
				MaxStreamsPerConnection: 50,
				StreamCreationRate:      200.0,
				MemoryBasedAllocation:   true,
				MemoryThreshold:         0.7,
				MemoryPressureThreshold: 0.85,
				DynamicBufferSizing:     true,
				MinBufferSize:           8 * 1024,   // 8KB
				MaxBufferSize:           512 * 1024, // 512KB
				BufferGrowthFactor:      1.5,
				BufferShrinkThreshold:   0.4, // 40%
				DynamicWindowSizing:     true,
				WindowShrinkRate:        0.8,
				WindowGrowthRate:        2.0,
				WindowUtilizationTarget: 0.6,
				AggressiveCleanup:       true,
				CleanupInterval:         10 * time.Second,
				IdleStreamTimeout:       1 * time.Minute,
				EnableResourceMonitoring: true,
				MonitoringInterval:       5 * time.Second,
				AdaptiveAllocation:      true,
				AdaptationInterval:      30 * time.Second,
				LearningRate:            0.2,
				OrphanedResourceTimeout: 5 * time.Minute,
				GlobalMemoryLimit:       50 * 1024 * 1024, // 50MB
				PerStreamMemoryLimit:    512 * 1024,       // 512KB
				ConnectionMemoryLimit:   5 * 1024 * 1024,  // 5MB
				PriorityBasedAllocation: true,
				PriorityWeights: map[StreamPriority]float64{
					StreamPriorityLow:      0.1,
					StreamPriorityNormal:   0.3,
					StreamPriorityHigh:     0.6,
					StreamPriorityCritical: 1.0,
				},
				PriorityReservation: map[StreamPriority]float64{
					StreamPriorityLow:      0.05,
					StreamPriorityNormal:   0.25,
					StreamPriorityHigh:     0.4,
					StreamPriorityCritical: 0.3,
				},
				FairnessEnabled:     true,
				QoSEnabled:          true,
				BandwidthAllocation: "priority_based",
			},
			Policy: &ResourcePolicy{
				AllocationStrategy:   AllocationStrategyPriorityBased,
				PreemptionEnabled:    true,
				PreemptionThreshold:  0.8,
				FairShareEnabled:     false,
				PriorityPreemption:   true,
				AdaptiveEnabled:      true,
				AdaptationThreshold:  0.7,
				AutoCleanupEnabled:   true,
				CleanupThreshold:     0.8,
				GracePeriod:          10 * time.Second,
			},
		},
		"high_throughput": {
			Name:        "High Throughput",
			Description: "Optimized for high throughput applications",
			Config: &ResourceManagementConfig{
				MaxConcurrentStreams:    2000,
				MaxStreamsPerConnection: 200,
				StreamCreationRate:      500.0,
				MemoryBasedAllocation:   true,
				MemoryThreshold:         0.85,
				MemoryPressureThreshold: 0.95,
				DynamicBufferSizing:     true,
				MinBufferSize:           16 * 1024,  // 16KB
				MaxBufferSize:           4 * 1024 * 1024, // 4MB
				BufferGrowthFactor:      2.0,
				BufferShrinkThreshold:   0.3, // 30%
				DynamicWindowSizing:     true,
				WindowGrowthRate:        1.8,
				WindowShrinkRate:        0.9,
				WindowUtilizationTarget: 0.8,
				AggressiveCleanup:       false,
				CleanupInterval:         60 * time.Second,
				IdleStreamTimeout:       10 * time.Minute,
				EnableResourceMonitoring: true,
				MonitoringInterval:       15 * time.Second,
				PreallocationEnabled:    true,
				PreallocationSize:       10 * 1024 * 1024, // 10MB
				LoadBalancingEnabled:    true,
				AdaptiveAllocation:      true,
				AdaptationInterval:      2 * time.Minute,
				LearningRate:            0.05,
				OrphanedResourceTimeout: 15 * time.Minute,
				GlobalMemoryLimit:       200 * 1024 * 1024, // 200MB
				PerStreamMemoryLimit:    2 * 1024 * 1024,   // 2MB
				ConnectionMemoryLimit:   20 * 1024 * 1024,  // 20MB
				PriorityBasedAllocation: true,
				PriorityWeights: map[StreamPriority]float64{
					StreamPriorityLow:      0.2,
					StreamPriorityNormal:   0.5,
					StreamPriorityHigh:     0.8,
					StreamPriorityCritical: 1.0,
				},
				PriorityReservation: map[StreamPriority]float64{
					StreamPriorityLow:      0.1,
					StreamPriorityNormal:   0.4,
					StreamPriorityHigh:     0.3,
					StreamPriorityCritical: 0.2,
				},
				FairnessEnabled:     true,
				QoSEnabled:          true,
				BandwidthAllocation: "weighted_fair_queuing",
			},
			Policy: &ResourcePolicy{
				AllocationStrategy:   AllocationStrategyWeightedFair,
				PreemptionEnabled:    false,
				FairShareEnabled:     true,
				MinimumGuarantee:     0.05,
				MaximumShare:         0.9,
				AdaptiveEnabled:      true,
				AdaptationThreshold:  0.85,
				AutoCleanupEnabled:   true,
				CleanupThreshold:     0.9,
				GracePeriod:          60 * time.Second,
			},
		},
		"memory_constrained": {
			Name:        "Memory Constrained",
			Description: "Optimized for memory-constrained environments",
			Config: &ResourceManagementConfig{
				MaxConcurrentStreams:    200,
				MaxStreamsPerConnection: 20,
				StreamCreationRate:      50.0,
				MemoryBasedAllocation:   true,
				MemoryThreshold:         0.6,
				MemoryPressureThreshold: 0.75,
				DynamicBufferSizing:     true,
				MinBufferSize:           2 * 1024,   // 2KB
				MaxBufferSize:           64 * 1024,  // 64KB
				BufferGrowthFactor:      1.2,
				BufferShrinkThreshold:   0.4,
				DynamicWindowSizing:     true,
				WindowGrowthRate:        1.2,
				WindowShrinkRate:        0.9,
				WindowUtilizationTarget: 0.5,
				AggressiveCleanup:       true,
				CleanupInterval:         15 * time.Second,
				IdleStreamTimeout:       2 * time.Minute,
				EnableResourceMonitoring: true,
				MonitoringInterval:       5 * time.Second,
				AdaptiveAllocation:      true,
				AdaptationInterval:      1 * time.Minute,
				LearningRate:            0.15,
				GlobalMemoryLimit:       20 * 1024 * 1024, // 20MB
				PerStreamMemoryLimit:    256 * 1024,       // 256KB
			},
			Policy: &ResourcePolicy{
				AllocationStrategy:   AllocationStrategyBestFit,
				PreemptionEnabled:    true,
				PreemptionThreshold:  0.7,
				FairShareEnabled:     true,
				MinimumGuarantee:     0.02,
				MaximumShare:         0.5,
				PriorityPreemption:   true,
				AdaptiveEnabled:      true,
				AdaptationThreshold:  0.6,
				AutoCleanupEnabled:   true,
				CleanupThreshold:     0.7,
				GracePeriod:          15 * time.Second,
			},
		},
	}
}

// ResourceConfigManager manages resource configuration
type ResourceConfigManager struct {
	config   *ResourceManagementConfig
	policy   *ResourcePolicy
	profiles map[string]*ResourceProfile
	mutex    sync.RWMutex
}

// NewResourceConfigManager creates a new resource configuration manager
func NewResourceConfigManager() *ResourceConfigManager {
	return &ResourceConfigManager{
		config:   DefaultResourceManagementConfig(),
		policy:   DefaultResourcePolicy(),
		profiles: PredefinedResourceProfiles(),
	}
}

// GetConfig returns the current resource management configuration
func (rcm *ResourceConfigManager) GetConfig() *ResourceManagementConfig {
	rcm.mutex.RLock()
	defer rcm.mutex.RUnlock()
	
	// Return a copy
	configCopy := *rcm.config
	return &configCopy
}

// SetConfig sets the resource management configuration
func (rcm *ResourceConfigManager) SetConfig(config *ResourceManagementConfig) error {
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	
	rcm.mutex.Lock()
	defer rcm.mutex.Unlock()
	
	rcm.config = config
	return nil
}

// GetPolicy returns the current resource policy
func (rcm *ResourceConfigManager) GetPolicy() *ResourcePolicy {
	rcm.mutex.RLock()
	defer rcm.mutex.RUnlock()
	
	// Return a copy
	policyCopy := *rcm.policy
	return &policyCopy
}

// SetPolicy sets the resource policy
func (rcm *ResourceConfigManager) SetPolicy(policy *ResourcePolicy) {
	rcm.mutex.Lock()
	defer rcm.mutex.Unlock()
	
	rcm.policy = policy
}

// ApplyProfile applies a predefined resource profile
func (rcm *ResourceConfigManager) ApplyProfile(profileName string) error {
	rcm.mutex.Lock()
	defer rcm.mutex.Unlock()
	
	profile, exists := rcm.profiles[profileName]
	if !exists {
		return fmt.Errorf("profile '%s' not found", profileName)
	}
	
	if err := profile.Config.Validate(); err != nil {
		return fmt.Errorf("invalid profile configuration: %w", err)
	}
	
	rcm.config = profile.Config
	rcm.policy = profile.Policy
	
	return nil
}

// GetAvailableProfiles returns the names of available profiles
func (rcm *ResourceConfigManager) GetAvailableProfiles() []string {
	rcm.mutex.RLock()
	defer rcm.mutex.RUnlock()
	
	profiles := make([]string, 0, len(rcm.profiles))
	for name := range rcm.profiles {
		profiles = append(profiles, name)
	}
	return profiles
}

// GetProfile returns a specific profile
func (rcm *ResourceConfigManager) GetProfile(name string) (*ResourceProfile, error) {
	rcm.mutex.RLock()
	defer rcm.mutex.RUnlock()
	
	profile, exists := rcm.profiles[name]
	if !exists {
		return nil, fmt.Errorf("profile '%s' not found", name)
	}
	
	// Return a copy
	profileCopy := *profile
	configCopy := *profile.Config
	policyCopy := *profile.Policy
	profileCopy.Config = &configCopy
	profileCopy.Policy = &policyCopy
	
	return &profileCopy, nil
}

// AddCustomProfile adds a custom resource profile
func (rcm *ResourceConfigManager) AddCustomProfile(name string, profile *ResourceProfile) error {
	if err := profile.Config.Validate(); err != nil {
		return fmt.Errorf("invalid profile configuration: %w", err)
	}
	
	rcm.mutex.Lock()
	defer rcm.mutex.Unlock()
	
	rcm.profiles[name] = profile
	return nil
}