package presentation

import (
	"testing"
	"time"
)

func TestResourceManagementConfigValidation(t *testing.T) {
	// Test valid configuration
	config := DefaultResourceManagementConfig()
	if err := config.Validate(); err != nil {
		t.Errorf("Default configuration should be valid: %v", err)
	}
	
	// Test invalid configurations
	testCases := []struct {
		name        string
		modifyFunc  func(*ResourceManagementConfig)
		expectError bool
	}{
		{
			name: "negative max concurrent streams",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.MaxConcurrentStreams = -1
			},
			expectError: true,
		},
		{
			name: "invalid memory threshold",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.MemoryThreshold = 1.5
			},
			expectError: true,
		},
		{
			name: "memory pressure threshold less than memory threshold",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.MemoryThreshold = 0.8
				c.MemoryPressureThreshold = 0.7
			},
			expectError: true,
		},
		{
			name: "zero min buffer size",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.MinBufferSize = 0
			},
			expectError: true,
		},
		{
			name: "max buffer size less than min",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.MinBufferSize = 1024
				c.MaxBufferSize = 512
			},
			expectError: true,
		},
		{
			name: "invalid buffer growth factor",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.BufferGrowthFactor = 0.5
			},
			expectError: true,
		},
		{
			name: "invalid window utilization target",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.WindowUtilizationTarget = 1.5
			},
			expectError: true,
		},
		{
			name: "negative cleanup interval",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.CleanupInterval = -1 * time.Second
			},
			expectError: true,
		},
		{
			name: "invalid learning rate",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.LearningRate = 1.5
			},
			expectError: true,
		},
		{
			name: "zero global memory limit",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.GlobalMemoryLimit = 0
			},
			expectError: true,
		},
		{
			name: "negative priority weight",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.PriorityWeights[StreamPriorityNormal] = -0.5
			},
			expectError: true,
		},
		{
			name: "priority reservations exceed 1.0",
			modifyFunc: func(c *ResourceManagementConfig) {
				c.PriorityReservation[StreamPriorityLow] = 0.6
				c.PriorityReservation[StreamPriorityNormal] = 0.6
			},
			expectError: true,
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := DefaultResourceManagementConfig()
			tc.modifyFunc(config)
			
			err := config.Validate()
			if tc.expectError && err == nil {
				t.Errorf("Expected validation error but got none")
			} else if !tc.expectError && err != nil {
				t.Errorf("Unexpected validation error: %v", err)
			}
		})
	}
}

func TestResourceAllocationStrategy(t *testing.T) {
	strategies := []ResourceAllocationStrategy{
		AllocationStrategyFirstFit,
		AllocationStrategyBestFit,
		AllocationStrategyWorstFit,
		AllocationStrategyRoundRobin,
		AllocationStrategyWeightedFair,
		AllocationStrategyPriorityBased,
		AllocationStrategyAdaptive,
	}
	
	expectedStrings := []string{
		"first_fit",
		"best_fit",
		"worst_fit",
		"round_robin",
		"weighted_fair",
		"priority_based",
		"adaptive",
	}
	
	for i, strategy := range strategies {
		if strategy.String() != expectedStrings[i] {
			t.Errorf("Expected strategy %d to be '%s', got '%s'", 
				i, expectedStrings[i], strategy.String())
		}
	}
	
	// Test unknown strategy
	unknownStrategy := ResourceAllocationStrategy(999)
	if unknownStrategy.String() != "unknown" {
		t.Errorf("Expected unknown strategy to return 'unknown', got '%s'", 
			unknownStrategy.String())
	}
}

func TestResourceConfigManager(t *testing.T) {
	manager := NewResourceConfigManager()
	
	// Test getting default config
	config := manager.GetConfig()
	if config == nil {
		t.Fatal("Expected default config to be non-nil")
	}
	
	if err := config.Validate(); err != nil {
		t.Errorf("Default config should be valid: %v", err)
	}
	
	// Test setting new config
	newConfig := DefaultResourceManagementConfig()
	newConfig.MaxConcurrentStreams = 2000
	
	err := manager.SetConfig(newConfig)
	if err != nil {
		t.Errorf("Failed to set valid config: %v", err)
	}
	
	updatedConfig := manager.GetConfig()
	if updatedConfig.MaxConcurrentStreams != 2000 {
		t.Errorf("Expected MaxConcurrentStreams to be 2000, got %d", 
			updatedConfig.MaxConcurrentStreams)
	}
	
	// Test setting invalid config
	invalidConfig := DefaultResourceManagementConfig()
	invalidConfig.MaxConcurrentStreams = -1
	
	err = manager.SetConfig(invalidConfig)
	if err == nil {
		t.Error("Expected error when setting invalid config")
	}
	
	// Test getting and setting policy
	policy := manager.GetPolicy()
	if policy == nil {
		t.Fatal("Expected default policy to be non-nil")
	}
	
	newPolicy := DefaultResourcePolicy()
	newPolicy.PreemptionEnabled = false
	manager.SetPolicy(newPolicy)
	
	updatedPolicy := manager.GetPolicy()
	if updatedPolicy.PreemptionEnabled {
		t.Error("Expected PreemptionEnabled to be false")
	}
}

func TestResourceProfiles(t *testing.T) {
	manager := NewResourceConfigManager()
	
	// Test getting available profiles
	profiles := manager.GetAvailableProfiles()
	if len(profiles) == 0 {
		t.Error("Expected at least one predefined profile")
	}
	
	expectedProfiles := []string{"low_latency", "high_throughput", "memory_constrained"}
	for _, expected := range expectedProfiles {
		found := false
		for _, profile := range profiles {
			if profile == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected profile '%s' not found", expected)
		}
	}
	
	// Test getting specific profile
	profile, err := manager.GetProfile("low_latency")
	if err != nil {
		t.Errorf("Failed to get low_latency profile: %v", err)
	}
	
	if profile.Name != "Low Latency" {
		t.Errorf("Expected profile name 'Low Latency', got '%s'", profile.Name)
	}
	
	if err := profile.Config.Validate(); err != nil {
		t.Errorf("Profile config should be valid: %v", err)
	}
	
	// Test getting non-existent profile
	_, err = manager.GetProfile("non_existent")
	if err == nil {
		t.Error("Expected error when getting non-existent profile")
	}
	
	// Test applying profile
	err = manager.ApplyProfile("high_throughput")
	if err != nil {
		t.Errorf("Failed to apply high_throughput profile: %v", err)
	}
	
	config := manager.GetConfig()
	if config.MaxConcurrentStreams != 2000 {
		t.Errorf("Expected MaxConcurrentStreams to be 2000 after applying high_throughput profile, got %d", 
			config.MaxConcurrentStreams)
	}
	
	// Test applying non-existent profile
	err = manager.ApplyProfile("non_existent")
	if err == nil {
		t.Error("Expected error when applying non-existent profile")
	}
}

func TestCustomResourceProfile(t *testing.T) {
	manager := NewResourceConfigManager()
	
	// Create custom profile
	customConfig := DefaultResourceManagementConfig()
	customConfig.MaxConcurrentStreams = 5000
	customConfig.MaxStreamsPerConnection = 500
	
	customPolicy := DefaultResourcePolicy()
	customPolicy.AllocationStrategy = AllocationStrategyAdaptive
	
	customProfile := &ResourceProfile{
		Name:        "Custom High Performance",
		Description: "Custom profile for high performance scenarios",
		Config:      customConfig,
		Policy:      customPolicy,
	}
	
	// Add custom profile
	err := manager.AddCustomProfile("custom_high_perf", customProfile)
	if err != nil {
		t.Errorf("Failed to add custom profile: %v", err)
	}
	
	// Verify custom profile was added
	profiles := manager.GetAvailableProfiles()
	found := false
	for _, profile := range profiles {
		if profile == "custom_high_perf" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Custom profile not found in available profiles")
	}
	
	// Get and verify custom profile
	retrievedProfile, err := manager.GetProfile("custom_high_perf")
	if err != nil {
		t.Errorf("Failed to get custom profile: %v", err)
	}
	
	if retrievedProfile.Name != "Custom High Performance" {
		t.Errorf("Expected custom profile name 'Custom High Performance', got '%s'", 
			retrievedProfile.Name)
	}
	
	if retrievedProfile.Config.MaxConcurrentStreams != 5000 {
		t.Errorf("Expected MaxConcurrentStreams to be 5000, got %d", 
			retrievedProfile.Config.MaxConcurrentStreams)
	}
	
	// Apply custom profile
	err = manager.ApplyProfile("custom_high_perf")
	if err != nil {
		t.Errorf("Failed to apply custom profile: %v", err)
	}
	
	config := manager.GetConfig()
	if config.MaxConcurrentStreams != 5000 {
		t.Errorf("Expected MaxConcurrentStreams to be 5000 after applying custom profile, got %d", 
			config.MaxConcurrentStreams)
	}
	
	policy := manager.GetPolicy()
	if policy.AllocationStrategy != AllocationStrategyAdaptive {
		t.Errorf("Expected AllocationStrategy to be Adaptive, got %s", 
			policy.AllocationStrategy.String())
	}
}

func TestResourceProfileValidation(t *testing.T) {
	manager := NewResourceConfigManager()
	
	// Test adding profile with invalid config
	invalidConfig := DefaultResourceManagementConfig()
	invalidConfig.MaxConcurrentStreams = -1
	
	invalidProfile := &ResourceProfile{
		Name:        "Invalid Profile",
		Description: "Profile with invalid configuration",
		Config:      invalidConfig,
		Policy:      DefaultResourcePolicy(),
	}
	
	err := manager.AddCustomProfile("invalid", invalidProfile)
	if err == nil {
		t.Error("Expected error when adding profile with invalid config")
	}
}

func TestPredefinedProfiles(t *testing.T) {
	profiles := PredefinedResourceProfiles()
	
	// Test that all predefined profiles have valid configurations
	for name, profile := range profiles {
		t.Run(name, func(t *testing.T) {
			if profile.Config == nil {
				t.Fatal("Profile config is nil")
			}
			
			if profile.Policy == nil {
				t.Fatal("Profile policy is nil")
			}
			
			if err := profile.Config.Validate(); err != nil {
				t.Errorf("Profile config is invalid: %v", err)
			}
			
			if profile.Name == "" {
				t.Error("Profile name is empty")
			}
			
			if profile.Description == "" {
				t.Error("Profile description is empty")
			}
		})
	}
	
	// Test specific profile characteristics
	lowLatency := profiles["low_latency"]
	if lowLatency.Config.MaxConcurrentStreams != 500 {
		t.Errorf("Expected low_latency MaxConcurrentStreams to be 500, got %d", 
			lowLatency.Config.MaxConcurrentStreams)
	}
	
	if !lowLatency.Config.AggressiveCleanup {
		t.Error("Expected low_latency to have aggressive cleanup enabled")
	}
	
	highThroughput := profiles["high_throughput"]
	if highThroughput.Config.MaxConcurrentStreams != 2000 {
		t.Errorf("Expected high_throughput MaxConcurrentStreams to be 2000, got %d", 
			highThroughput.Config.MaxConcurrentStreams)
	}
	
	if !highThroughput.Config.PreallocationEnabled {
		t.Error("Expected high_throughput to have preallocation enabled")
	}
	
	memoryConstrained := profiles["memory_constrained"]
	if memoryConstrained.Config.MaxConcurrentStreams != 200 {
		t.Errorf("Expected memory_constrained MaxConcurrentStreams to be 200, got %d", 
			memoryConstrained.Config.MaxConcurrentStreams)
	}
	
	if memoryConstrained.Config.GlobalMemoryLimit != 20*1024*1024 {
		t.Errorf("Expected memory_constrained GlobalMemoryLimit to be 20MB, got %d", 
			memoryConstrained.Config.GlobalMemoryLimit)
	}
}

func BenchmarkResourceConfigValidation(b *testing.B) {
	config := DefaultResourceManagementConfig()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = config.Validate()
	}
}

func BenchmarkResourceConfigManagerOperations(b *testing.B) {
	manager := NewResourceConfigManager()
	config := DefaultResourceManagementConfig()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.SetConfig(config)
		_ = manager.GetConfig()
		_ = manager.GetPolicy()
	}
}