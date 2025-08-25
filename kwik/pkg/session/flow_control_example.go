package session

import (
	"fmt"
	"time"
)

// FlowControlExample demonstrates how to use the flow control system
type FlowControlExample struct {
	session            Session
	flowControlManager *FlowControlManager
	sender             *FlowControlledSender
	receiver           *FlowControlledReceiver
	logger             SessionLogger
}

// NewFlowControlExample creates a new flow control example
func NewFlowControlExample(session Session, logger SessionLogger) *FlowControlExample {
	var flowControlManager *FlowControlManager
	
	// Get flow control manager from session
	if clientSession, ok := session.(*ClientSession); ok {
		flowControlManager = clientSession.flowControlManager
	} else if serverSession, ok := session.(*ServerSession); ok {
		flowControlManager = serverSession.flowControlManager
	}
	
	if flowControlManager == nil {
		logger.Error("Flow control manager not available")
		return nil
	}
	
	return &FlowControlExample{
		session:            session,
		flowControlManager: flowControlManager,
		sender:             NewFlowControlledSender(flowControlManager, logger),
		receiver:           NewFlowControlledReceiver(flowControlManager, logger),
		logger:             logger,
	}
}

// SendDataWithFlowControl demonstrates sending data with flow control
func (fce *FlowControlExample) SendDataWithFlowControl(pathID string, data []byte) error {
	fce.logger.Info("Sending data with flow control", "pathID", pathID, "dataSize", len(data))
	
	// Define the actual send function (this would be your real send implementation)
	sendFunc := func(data []byte) error {
		// This is where you would actually send the data over the network
		// For this example, we'll just simulate a successful send
		fce.logger.Debug("Data sent successfully", "dataSize", len(data))
		return nil
	}
	
	// Send data with flow control
	err := fce.sender.SendData(pathID, data, sendFunc)
	if err != nil {
		return fmt.Errorf("failed to send data with flow control: %w", err)
	}
	
	fce.logger.Info("Data sent successfully with flow control", "pathID", pathID, "dataSize", len(data))
	return nil
}

// SendDataWithRetry demonstrates sending data with flow control and retry logic
func (fce *FlowControlExample) SendDataWithRetry(pathID string, data []byte) error {
	fce.logger.Info("Sending data with flow control and retry", "pathID", pathID, "dataSize", len(data))
	
	// Define the actual send function with potential failures
	sendFunc := func(data []byte) error {
		// Simulate occasional send failures
		if time.Now().UnixNano()%3 == 0 {
			return fmt.Errorf("simulated send failure")
		}
		
		fce.logger.Debug("Data sent successfully", "dataSize", len(data))
		return nil
	}
	
	// Send data with flow control and retry
	maxRetries := 3
	retryDelay := 100 * time.Millisecond
	
	err := fce.sender.SendDataWithRetry(pathID, data, sendFunc, maxRetries, retryDelay)
	if err != nil {
		return fmt.Errorf("failed to send data with retry: %w", err)
	}
	
	fce.logger.Info("Data sent successfully with retry", "pathID", pathID, "dataSize", len(data))
	return nil
}

// ReceiveDataWithFlowControl demonstrates receiving data with flow control
func (fce *FlowControlExample) ReceiveDataWithFlowControl(pathID string, data []byte) error {
	fce.logger.Info("Receiving data with flow control", "pathID", pathID, "dataSize", len(data))
	
	// Notify flow control about received data
	err := fce.receiver.ReceiveData(pathID, data)
	if err != nil {
		return fmt.Errorf("flow control rejected data: %w", err)
	}
	
	// Simulate processing the data
	time.Sleep(10 * time.Millisecond)
	
	// Notify flow control that data has been consumed
	err = fce.receiver.ConsumeData(pathID, uint64(len(data)))
	if err != nil {
		fce.logger.Warn("Failed to notify data consumption", "error", err)
	}
	
	fce.logger.Info("Data received and processed with flow control", "pathID", pathID, "dataSize", len(data))
	return nil
}

// MonitorFlowControl demonstrates monitoring flow control statistics
func (fce *FlowControlExample) MonitorFlowControl() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			stats := fce.flowControlManager.GetFlowControlStats()
			
			fce.logger.Info("Flow control statistics",
				"windowUpdatesSent", stats.WindowUpdatesSent,
				"windowUpdatesReceived", stats.WindowUpdatesReceived,
				"backpressureEvents", stats.BackpressureEvents,
				"totalDataSent", stats.TotalDataSent,
				"totalDataReceived", stats.TotalDataReceived,
				"activePaths", stats.ActivePaths,
				"averageWindowUtilization", stats.AverageWindowUtilization,
			)
			
			// Log per-path statistics
			for pathID, updateCount := range stats.PathWindowUpdates {
				fce.logger.Debug("Path window updates", "pathID", pathID, "updates", updateCount)
			}
			
		case <-time.After(30 * time.Second):
			// Stop monitoring after 30 seconds for this example
			fce.logger.Info("Stopping flow control monitoring")
			return
		}
	}
}

// DemonstrateMultiPathFlowControl shows how flow control works with multiple paths
func (fce *FlowControlExample) DemonstrateMultiPathFlowControl(pathIDs []string, dataSize int) error {
	fce.logger.Info("Demonstrating multi-path flow control", "paths", len(pathIDs), "dataSize", dataSize)
	
	// Create test data
	data := make([]byte, dataSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	
	// Send data on each path
	for i, pathID := range pathIDs {
		// Add some variation to the data for each path
		pathData := make([]byte, len(data))
		copy(pathData, data)
		pathData[0] = byte(i) // Mark with path index
		
		err := fce.SendDataWithFlowControl(pathID, pathData)
		if err != nil {
			fce.logger.Error("Failed to send data on path", "pathID", pathID, "error", err)
			continue
		}
		
		// Small delay between sends
		time.Sleep(50 * time.Millisecond)
	}
	
	fce.logger.Info("Multi-path flow control demonstration completed")
	return nil
}

// DemonstrateBackpressureHandling shows how backpressure is handled
func (fce *FlowControlExample) DemonstrateBackpressureHandling(pathID string) error {
	fce.logger.Info("Demonstrating backpressure handling", "pathID", pathID)
	
	// Send large amounts of data to trigger backpressure
	largeData := make([]byte, 2*1024*1024) // 2MB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	
	// Send data in chunks
	chunkSize := 64 * 1024 // 64KB chunks
	for i := 0; i < len(largeData); i += chunkSize {
		end := i + chunkSize
		if end > len(largeData) {
			end = len(largeData)
		}
		
		chunk := largeData[i:end]
		
		fce.logger.Debug("Sending chunk", "chunkIndex", i/chunkSize, "chunkSize", len(chunk))
		
		err := fce.SendDataWithRetry(pathID, chunk)
		if err != nil {
			fce.logger.Error("Failed to send chunk", "chunkIndex", i/chunkSize, "error", err)
			return err
		}
		
		// Small delay to allow flow control to work
		time.Sleep(10 * time.Millisecond)
	}
	
	fce.logger.Info("Backpressure handling demonstration completed")
	return nil
}

// GetFlowControlStats returns current flow control statistics
func (fce *FlowControlExample) GetFlowControlStats() *FlowControlStats {
	return fce.flowControlManager.GetFlowControlStats()
}

// SetupFlowControlCallbacks sets up callbacks for flow control events
func (fce *FlowControlExample) SetupFlowControlCallbacks() {
	fce.flowControlManager.SetWindowUpdateCallback(func(pathID string, windowSize uint64) {
		fce.logger.Info("Window update callback", "pathID", pathID, "windowSize", windowSize)
	})
	
	fce.flowControlManager.SetBackpressureCallback(func(pathID string, active bool) {
		if active {
			fce.logger.Warn("Backpressure activated", "pathID", pathID)
		} else {
			fce.logger.Info("Backpressure released", "pathID", pathID)
		}
	})
	
	fce.flowControlManager.SetWindowFullCallback(func() {
		fce.logger.Warn("Receive window is full - system-wide backpressure")
	})
	
	fce.flowControlManager.SetWindowAvailableCallback(func() {
		fce.logger.Info("Receive window is available - backpressure released")
	})
}