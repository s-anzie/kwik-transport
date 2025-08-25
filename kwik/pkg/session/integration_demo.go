package session

import (
	"fmt"
	"time"

	"kwik/pkg/protocol"
)

// DemonstrateAdaptiveSchedulingIntegration shows how the adaptive scheduler integrates with heartbeat systems
func DemonstrateAdaptiveSchedulingIntegration() {
	fmt.Println("=== Adaptive Heartbeat Scheduling Integration Demo ===")
	
	// Create scheduler with custom configuration
	config := &HeartbeatSchedulingConfig{
		MinControlInterval:        5 * time.Second,
		MaxControlInterval:        60 * time.Second,
		MinDataInterval:          10 * time.Second,
		MaxDataInterval:          120 * time.Second,
		RTTMultiplier:            3.0,
		FailureAdaptationFactor:  0.5,
		RecoveryAdaptationFactor: 1.2,
		ActivityThreshold:        30 * time.Second,
		GoodNetworkThreshold:     0.8,
		PoorNetworkThreshold:     0.3,
	}
	
	scheduler := NewHeartbeatScheduler(config, nil)
	defer scheduler.Shutdown()
	
	integration := NewHeartbeatSchedulerIntegration(scheduler, nil)
	defer integration.Shutdown()
	
	pathID := "demo-path"
	
	fmt.Printf("\n1. Initial intervals:\n")
	controlInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)
	streamID := uint64(100)
	dataInterval := scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)
	fmt.Printf("   Control: %v, Data: %v\n", controlInterval, dataInterval)
	
	fmt.Printf("\n2. Simulating good network conditions (RTT: 30ms, Loss: 0%%):\n")
	integration.UpdateNetworkFeedback(pathID, 30*time.Millisecond, 0.0, 0)
	controlInterval = scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)
	dataInterval = scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)
	fmt.Printf("   Control: %v, Data: %v\n", controlInterval, dataInterval)
	
	fmt.Printf("\n3. Simulating poor network conditions (RTT: 400ms, Loss: 15%%, Failures: 5):\n")
	integration.UpdateNetworkFeedback(pathID, 400*time.Millisecond, 0.15, 5)
	controlInterval = scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)
	dataInterval = scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)
	fmt.Printf("   Control: %v, Data: %v\n", controlInterval, dataInterval)
	
	fmt.Printf("\n4. Simulating active stream (recent activity):\n")
	integration.UpdateStreamActivity(streamID, pathID, time.Now().Add(-5*time.Second))
	dataInterval = scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)
	fmt.Printf("   Data (active stream): %v\n", dataInterval)
	
	fmt.Printf("\n5. Simulating inactive stream (old activity):\n")
	integration.UpdateStreamActivity(streamID, pathID, time.Now().Add(-60*time.Second))
	dataInterval = scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)
	fmt.Printf("   Data (inactive stream): %v\n", dataInterval)
	
	fmt.Printf("\n6. Scheduling parameters:\n")
	params := scheduler.GetSchedulingParams(pathID)
	if params != nil {
		fmt.Printf("   Network Score: %.2f\n", params.NetworkConditionScore)
		fmt.Printf("   Recent Failures: %d\n", params.RecentFailureCount)
		fmt.Printf("   Control Interval: %v\n", params.CurrentControlInterval)
		fmt.Printf("   Data Interval: %v\n", params.CurrentDataInterval)
	}
	
	fmt.Printf("\n=== Demo Complete ===\n")
}