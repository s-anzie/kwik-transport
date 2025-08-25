package presentation

import (
	"testing"
	"time"
)

func TestResourceManagementIntegration(t *testing.T) {
	// Create DPM with large window sizes for testing
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 50 * 1024 * 1024 // 50MB
	config.DefaultStreamBufferSize = 128 * 1024 // 128KB per stream
	dpm := NewDataPresentationManager(config)
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	// Test getting resource configuration manager
	rcm := dpm.GetResourceConfigManager()
	if rcm == nil {
		t.Fatal("Expected resource configuration manager to be available")
	}
	
	// Test getting default configuration
	resourceConfig := dpm.GetResourceManagementConfig()
	if resourceConfig == nil {
		t.Fatal("Expected resource management config to be available")
	}
	
	if resourceConfig.MaxConcurrentStreams != 1000 {
		t.Errorf("Expected default MaxConcurrentStreams to be 1000, got %d", 
			resourceConfig.MaxConcurrentStreams)
	}
	
	// Test getting available profiles
	profiles := dpm.GetAvailableResourceProfiles()
	if len(profiles) == 0 {
		t.Fatal("Expected at least one resource profile to be available")
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
			t.Errorf("Expected profile '%s' not found in available profiles", expected)
		}
	}
	
	// Test applying low latency profile
	err = dpm.ApplyResourceProfile("low_latency")
	if err != nil {
		t.Errorf("Failed to apply low_latency profile: %v", err)
	}
	
	// Verify profile was applied
	updatedConfig := dpm.GetResourceManagementConfig()
	if updatedConfig.MaxConcurrentStreams != 500 {
		t.Errorf("Expected MaxConcurrentStreams to be 500 after applying low_latency profile, got %d", 
			updatedConfig.MaxConcurrentStreams)
	}
	
	if !updatedConfig.AggressiveCleanup {
		t.Error("Expected AggressiveCleanup to be enabled in low_latency profile")
	}
	
	// Test getting specific profile
	profile, err := dpm.GetResourceProfile("high_throughput")
	if err != nil {
		t.Errorf("Failed to get high_throughput profile: %v", err)
	}
	
	if profile.Name != "High Throughput" {
		t.Errorf("Expected profile name 'High Throughput', got '%s'", profile.Name)
	}
	
	// Test applying high throughput profile
	err = dpm.ApplyResourceProfile("high_throughput")
	if err != nil {
		t.Errorf("Failed to apply high_throughput profile: %v", err)
	}
	
	// Verify high throughput profile settings
	htConfig := dpm.GetResourceManagementConfig()
	if htConfig.MaxConcurrentStreams != 2000 {
		t.Errorf("Expected MaxConcurrentStreams to be 2000 after applying high_throughput profile, got %d", 
			htConfig.MaxConcurrentStreams)
	}
	
	if !htConfig.PreallocationEnabled {
		t.Error("Expected PreallocationEnabled to be true in high_throughput profile")
	}
	
	// Test resource policy
	policy := dpm.GetResourcePolicy()
	if policy == nil {
		t.Fatal("Expected resource policy to be available")
	}
	
	if policy.AllocationStrategy != AllocationStrategyWeightedFair {
		t.Errorf("Expected AllocationStrategy to be WeightedFair, got %s", 
			policy.AllocationStrategy.String())
	}
	
	// Test custom configuration
	customConfig := DefaultResourceManagementConfig()
	customConfig.MaxConcurrentStreams = 3000
	customConfig.MemoryThreshold = 0.75
	
	err = dpm.SetResourceManagementConfig(customConfig)
	if err != nil {
		t.Errorf("Failed to set custom resource management config: %v", err)
	}
	
	// Verify custom configuration was applied
	finalConfig := dpm.GetResourceManagementConfig()
	if finalConfig.MaxConcurrentStreams != 3000 {
		t.Errorf("Expected MaxConcurrentStreams to be 3000 after setting custom config, got %d", 
			finalConfig.MaxConcurrentStreams)
	}
	
	if finalConfig.MemoryThreshold != 0.75 {
		t.Errorf("Expected MemoryThreshold to be 0.75 after setting custom config, got %f", 
			finalConfig.MemoryThreshold)
	}
}

func TestCustomResourceProfileIntegration(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 20 * 1024 * 1024 // 20MB
	dpm := NewDataPresentationManager(config)
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	// Create custom profile
	customConfig := DefaultResourceManagementConfig()
	customConfig.MaxConcurrentStreams = 5000
	customConfig.MaxStreamsPerConnection = 500
	customConfig.MemoryThreshold = 0.9
	customConfig.AggressiveCleanup = true
	customConfig.CleanupInterval = 5 * time.Second
	
	customPolicy := DefaultResourcePolicy()
	customPolicy.AllocationStrategy = AllocationStrategyAdaptive
	customPolicy.PreemptionEnabled = true
	customPolicy.PreemptionThreshold = 0.85
	
	customProfile := &ResourceProfile{
		Name:        "Custom Ultra Performance",
		Description: "Custom profile for ultra high performance scenarios",
		Config:      customConfig,
		Policy:      customPolicy,
	}
	
	// Add custom profile
	err = dpm.AddCustomResourceProfile("ultra_performance", customProfile)
	if err != nil {
		t.Errorf("Failed to add custom resource profile: %v", err)
	}
	
	// Verify custom profile was added
	profiles := dpm.GetAvailableResourceProfiles()
	found := false
	for _, profile := range profiles {
		if profile == "ultra_performance" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Custom profile 'ultra_performance' not found in available profiles")
	}
	
	// Get and verify custom profile
	retrievedProfile, err := dpm.GetResourceProfile("ultra_performance")
	if err != nil {
		t.Errorf("Failed to get custom profile: %v", err)
	}
	
	if retrievedProfile.Name != "Custom Ultra Performance" {
		t.Errorf("Expected custom profile name 'Custom Ultra Performance', got '%s'", 
			retrievedProfile.Name)
	}
	
	if retrievedProfile.Config.MaxConcurrentStreams != 5000 {
		t.Errorf("Expected MaxConcurrentStreams to be 5000, got %d", 
			retrievedProfile.Config.MaxConcurrentStreams)
	}
	
	// Apply custom profile
	err = dpm.ApplyResourceProfile("ultra_performance")
	if err != nil {
		t.Errorf("Failed to apply custom profile: %v", err)
	}
	
	// Verify custom profile was applied
	appliedConfig := dpm.GetResourceManagementConfig()
	if appliedConfig.MaxConcurrentStreams != 5000 {
		t.Errorf("Expected MaxConcurrentStreams to be 5000 after applying custom profile, got %d", 
			appliedConfig.MaxConcurrentStreams)
	}
	
	if appliedConfig.MemoryThreshold != 0.9 {
		t.Errorf("Expected MemoryThreshold to be 0.9 after applying custom profile, got %f", 
			appliedConfig.MemoryThreshold)
	}
	
	appliedPolicy := dpm.GetResourcePolicy()
	if appliedPolicy.AllocationStrategy != AllocationStrategyAdaptive {
		t.Errorf("Expected AllocationStrategy to be Adaptive after applying custom profile, got %s", 
			appliedPolicy.AllocationStrategy.String())
	}
	
	if appliedPolicy.PreemptionThreshold != 0.85 {
		t.Errorf("Expected PreemptionThreshold to be 0.85 after applying custom profile, got %f", 
			appliedPolicy.PreemptionThreshold)
	}
}

func TestResourceManagementWithStreams(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 100 * 1024 * 1024 // 100MB
	config.DefaultStreamBufferSize = 64 * 1024   // 64KB per stream
	dpm := NewDataPresentationManager(config)
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	// Apply memory constrained profile
	err = dpm.ApplyResourceProfile("memory_constrained")
	if err != nil {
		t.Errorf("Failed to apply memory_constrained profile: %v", err)
	}
	
	// Verify profile limits
	resourceConfig := dpm.GetResourceManagementConfig()
	if resourceConfig.MaxConcurrentStreams != 200 {
		t.Errorf("Expected MaxConcurrentStreams to be 200 in memory_constrained profile, got %d", 
			resourceConfig.MaxConcurrentStreams)
	}
	
	// Create streams up to the limit
	maxStreams := 10 // Use a smaller number for testing
	for i := uint64(1); i <= uint64(maxStreams); i++ {
		metadata := &StreamMetadata{
			StreamID:     i,
			Priority:     StreamPriorityNormal,
			LastActivity: time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(i, metadata)
		if err != nil {
			t.Errorf("Failed to create stream %d: %v", i, err)
		}
	}
	
	// Verify streams were created
	stats := dpm.GetGlobalStats()
	if stats.ActiveStreams != maxStreams {
		t.Errorf("Expected %d active streams, got %d", maxStreams, stats.ActiveStreams)
	}
	
	// Test resource monitoring with the applied configuration
	resourceMetrics := dpm.GetResourceMetrics()
	if resourceMetrics != nil {
		t.Logf("Active streams: %d", resourceMetrics.ActiveStreams)
		t.Logf("Memory utilization: %.2f%%", resourceMetrics.MemoryUtilization*100)
		t.Logf("Window utilization: %.2f%%", resourceMetrics.WindowUtilization*100)
		
		if resourceMetrics.ActiveStreams != maxStreams {
			t.Errorf("Expected resource metrics to show %d active streams, got %d", 
				maxStreams, resourceMetrics.ActiveStreams)
		}
	}
	
	// Test resource cleanup
	gcStats := dpm.ForceResourceCleanup()
	if gcStats != nil {
		t.Logf("GC runs: %d, Resources freed: %d", gcStats.TotalGCRuns, gcStats.TotalResourcesFreed)
	}
	
	// Clean up streams
	for i := uint64(1); i <= uint64(maxStreams); i++ {
		err := dpm.RemoveStreamBuffer(i)
		if err != nil {
			t.Errorf("Failed to remove stream %d: %v", i, err)
		}
	}
	
	// Verify streams were removed
	finalStats := dpm.GetGlobalStats()
	if finalStats.ActiveStreams != 0 {
		t.Errorf("Expected 0 active streams after cleanup, got %d", finalStats.ActiveStreams)
	}
}

func TestResourcePolicyConfiguration(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 20 * 1024 * 1024 // 20MB
	dpm := NewDataPresentationManager(config)
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	// Test default policy
	defaultPolicy := dpm.GetResourcePolicy()
	if defaultPolicy == nil {
		t.Fatal("Expected default resource policy to be available")
	}
	
	if defaultPolicy.AllocationStrategy != AllocationStrategyWeightedFair {
		t.Errorf("Expected default AllocationStrategy to be WeightedFair, got %s", 
			defaultPolicy.AllocationStrategy.String())
	}
	
	// Test custom policy
	customPolicy := &ResourcePolicy{
		AllocationStrategy:   AllocationStrategyPriorityBased,
		PreemptionEnabled:    true,
		PreemptionThreshold:  0.8,
		FairShareEnabled:     false,
		PriorityPreemption:   true,
		PriorityInversion:    false,
		PriorityAging:        true,
		AgingFactor:          0.05,
		AdaptiveEnabled:      true,
		AdaptationThreshold:  0.75,
		AdaptationRate:       0.15,
		AutoCleanupEnabled:   true,
		CleanupThreshold:     0.85,
		GracePeriod:          20 * time.Second,
	}
	
	dpm.SetResourcePolicy(customPolicy)
	
	// Verify custom policy was applied
	appliedPolicy := dpm.GetResourcePolicy()
	if appliedPolicy.AllocationStrategy != AllocationStrategyPriorityBased {
		t.Errorf("Expected AllocationStrategy to be PriorityBased after setting custom policy, got %s", 
			appliedPolicy.AllocationStrategy.String())
	}
	
	if appliedPolicy.PreemptionThreshold != 0.8 {
		t.Errorf("Expected PreemptionThreshold to be 0.8 after setting custom policy, got %f", 
			appliedPolicy.PreemptionThreshold)
	}
	
	if appliedPolicy.AgingFactor != 0.05 {
		t.Errorf("Expected AgingFactor to be 0.05 after setting custom policy, got %f", 
			appliedPolicy.AgingFactor)
	}
	
	if appliedPolicy.GracePeriod != 20*time.Second {
		t.Errorf("Expected GracePeriod to be 20s after setting custom policy, got %v", 
			appliedPolicy.GracePeriod)
	}
}