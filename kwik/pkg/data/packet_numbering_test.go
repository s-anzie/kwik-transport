package data

import (
	"testing"
	"time"

	datapb "kwik/proto/data"
)

func TestPacketNumberingManager_RegisterPath(t *testing.T) {
	manager := NewPacketNumberingManager(nil)

	tests := []struct {
		name        string
		pathID      string
		expectError bool
	}{
		{
			name:        "valid path registration",
			pathID:      "path-1",
			expectError: false,
		},
		{
			name:        "another valid path",
			pathID:      "path-2",
			expectError: false,
		},
		{
			name:        "empty path ID",
			pathID:      "",
			expectError: true,
		},
		{
			name:        "duplicate path ID",
			pathID:      "path-1", // Already registered above
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.RegisterPath(tt.pathID)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Verify path was registered
			pathSpace, err := manager.GetPathNumberSpace(tt.pathID)
			if err != nil {
				t.Errorf("failed to get path number space: %v", err)
				return
			}

			if pathSpace.PathID != tt.pathID {
				t.Errorf("expected path ID %s, got %s", tt.pathID, pathSpace.PathID)
			}

			if pathSpace.NextPacketNumber != 1 {
				t.Errorf("expected initial packet number 1, got %d", pathSpace.NextPacketNumber)
			}

			if pathSpace.NextSequenceNumber != 1 {
				t.Errorf("expected initial sequence number 1, got %d", pathSpace.NextSequenceNumber)
			}
		})
	}
}

func TestPacketNumberingManager_AssignPacketNumber(t *testing.T) {
	manager := NewPacketNumberingManager(nil)

	pathID := "test-path"
	err := manager.RegisterPath(pathID)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	tests := []struct {
		name                   string
		pathID                 string
		expectedPacketNumber   uint64
		expectedSequenceNumber uint32
		expectError            bool
	}{
		{
			name:                   "first packet assignment",
			pathID:                 pathID,
			expectedPacketNumber:   1,
			expectedSequenceNumber: 1,
			expectError:            false,
		},
		{
			name:                   "second packet assignment",
			pathID:                 pathID,
			expectedPacketNumber:   2,
			expectedSequenceNumber: 2,
			expectError:            false,
		},
		{
			name:                   "third packet assignment",
			pathID:                 pathID,
			expectedPacketNumber:   3,
			expectedSequenceNumber: 3,
			expectError:            false,
		},
		{
			name:        "unregistered path",
			pathID:      "unknown-path",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packetNumber, sequenceNumber, err := manager.AssignPacketNumber(tt.pathID)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if packetNumber != tt.expectedPacketNumber {
				t.Errorf("expected packet number %d, got %d", tt.expectedPacketNumber, packetNumber)
			}

			if sequenceNumber != tt.expectedSequenceNumber {
				t.Errorf("expected sequence number %d, got %d", tt.expectedSequenceNumber, sequenceNumber)
			}
		})
	}
}

func TestPacketNumberingManager_RecordSentPacket(t *testing.T) {
	manager := NewPacketNumberingManager(nil)

	pathID := "test-path"
	err := manager.RegisterPath(pathID)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	tests := []struct {
		name             string
		pathID           string
		packetNumber     uint64
		sequenceNumber   uint32
		packetSize       uint32
		frameCount       int
		isRetransmission bool
		expectError      bool
	}{
		{
			name:             "record first packet",
			pathID:           pathID,
			packetNumber:     1,
			sequenceNumber:   1,
			packetSize:       1200,
			frameCount:       3,
			isRetransmission: false,
			expectError:      false,
		},
		{
			name:             "record second packet",
			pathID:           pathID,
			packetNumber:     2,
			sequenceNumber:   2,
			packetSize:       800,
			frameCount:       2,
			isRetransmission: false,
			expectError:      false,
		},
		{
			name:             "record retransmission",
			pathID:           pathID,
			packetNumber:     3,
			sequenceNumber:   3,
			packetSize:       1200,
			frameCount:       3,
			isRetransmission: true,
			expectError:      false,
		},
		{
			name:           "unregistered path",
			pathID:         "unknown-path",
			packetNumber:   1,
			sequenceNumber: 1,
			packetSize:     1200,
			frameCount:     1,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.RecordSentPacket(tt.pathID, tt.packetNumber, tt.sequenceNumber, tt.packetSize, tt.frameCount, tt.isRetransmission)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Verify packet was recorded
			pathSpace, err := manager.GetPathNumberSpace(tt.pathID)
			if err != nil {
				t.Errorf("failed to get path number space: %v", err)
				return
			}

			// Check sent packet info
			sentInfo, exists := pathSpace.SentPackets[tt.packetNumber]
			if !exists {
				t.Errorf("sent packet %d not found", tt.packetNumber)
				return
			}

			if sentInfo.PacketNumber != tt.packetNumber {
				t.Errorf("expected packet number %d, got %d", tt.packetNumber, sentInfo.PacketNumber)
			}

			if sentInfo.PacketSize != tt.packetSize {
				t.Errorf("expected packet size %d, got %d", tt.packetSize, sentInfo.PacketSize)
			}

			if sentInfo.IsRetransmission != tt.isRetransmission {
				t.Errorf("expected retransmission %t, got %t", tt.isRetransmission, sentInfo.IsRetransmission)
			}

			// Check pending packet info
			pendingInfo, exists := pathSpace.PendingPackets[tt.packetNumber]
			if !exists {
				t.Errorf("pending packet %d not found", tt.packetNumber)
				return
			}

			if pendingInfo.PacketNumber != tt.packetNumber {
				t.Errorf("expected pending packet number %d, got %d", tt.packetNumber, pendingInfo.PacketNumber)
			}
		})
	}
}

func TestPacketNumberingManager_ProcessAcknowledgment(t *testing.T) {
	manager := NewPacketNumberingManager(nil)

	pathID := "test-path"
	err := manager.RegisterPath(pathID)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	// Record some sent packets first
	for i := uint64(1); i <= 5; i++ {
		err := manager.RecordSentPacket(pathID, i, uint32(i), 1200, 2, false)
		if err != nil {
			t.Fatalf("failed to record sent packet %d: %v", i, err)
		}
	}

	// Create ACK frame
	ackFrame := &datapb.AckFrame{
		AckId:           1,
		AckedPacketIds:  []uint64{1, 3},
		PathId:          pathID,
		Timestamp:       uint64(time.Now().UnixNano()),
		LargestAcked:    3,
		AckDelay:        1000, // 1ms
		AckRanges: []*datapb.AckRange{
			{Start: 4, End: 5}, // ACK packets 4 and 5
		},
	}

	err = manager.ProcessAcknowledgment(pathID, ackFrame)
	if err != nil {
		t.Errorf("unexpected error processing ACK: %v", err)
		return
	}

	// Verify packets were acknowledged
	pathSpace, err := manager.GetPathNumberSpace(pathID)
	if err != nil {
		t.Fatalf("failed to get path number space: %v", err)
	}

	// Check that packets 1, 3, 4, 5 are no longer pending
	expectedAckedPackets := []uint64{1, 3, 4, 5}
	for _, packetNum := range expectedAckedPackets {
		if _, stillPending := pathSpace.PendingPackets[packetNum]; stillPending {
			t.Errorf("packet %d should not be pending after ACK", packetNum)
		}

		if _, isAcked := pathSpace.AckedPackets[packetNum]; !isAcked {
			t.Errorf("packet %d should be in acked packets", packetNum)
		}
	}

	// Check that packet 2 is still pending
	if _, stillPending := pathSpace.PendingPackets[2]; !stillPending {
		t.Errorf("packet 2 should still be pending")
	}

	// Check largest acked packet
	if pathSpace.LastAckedPacket != ackFrame.LargestAcked {
		t.Errorf("expected largest acked packet %d, got %d", ackFrame.LargestAcked, pathSpace.LastAckedPacket)
	}
}

func TestPacketNumberingManager_GetTimeoutPackets(t *testing.T) {
	// Use a config with very short timeout for testing
	config := DefaultPacketNumberingConfig()
	config.PacketTimeoutNs = 1000000 // 1ms timeout
	manager := NewPacketNumberingManager(config)

	pathID := "test-path"
	err := manager.RegisterPath(pathID)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	// Record some packets
	for i := uint64(1); i <= 3; i++ {
		err := manager.RecordSentPacket(pathID, i, uint32(i), 1200, 2, false)
		if err != nil {
			t.Fatalf("failed to record sent packet %d: %v", i, err)
		}
	}

	// Wait for timeout
	time.Sleep(2 * time.Millisecond)

	// Get timeout packets
	timeoutPackets, err := manager.GetTimeoutPackets(pathID)
	if err != nil {
		t.Errorf("unexpected error getting timeout packets: %v", err)
		return
	}

	// All packets should have timed out
	if len(timeoutPackets) != 3 {
		t.Errorf("expected 3 timeout packets, got %d", len(timeoutPackets))
	}

	// Verify packet numbers
	expectedPackets := map[uint64]bool{1: true, 2: true, 3: true}
	for _, timeoutPacket := range timeoutPackets {
		if !expectedPackets[timeoutPacket.PacketNumber] {
			t.Errorf("unexpected timeout packet number %d", timeoutPacket.PacketNumber)
		}
		delete(expectedPackets, timeoutPacket.PacketNumber)
	}

	if len(expectedPackets) > 0 {
		t.Errorf("missing timeout packets: %v", expectedPackets)
	}
}

func TestPacketNumberingManager_MarkPacketLost(t *testing.T) {
	manager := NewPacketNumberingManager(nil)

	pathID := "test-path"
	err := manager.RegisterPath(pathID)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	// Record a packet
	packetNumber := uint64(1)
	err = manager.RecordSentPacket(pathID, packetNumber, 1, 1200, 2, false)
	if err != nil {
		t.Fatalf("failed to record sent packet: %v", err)
	}

	// Verify packet is pending
	pathSpace, err := manager.GetPathNumberSpace(pathID)
	if err != nil {
		t.Fatalf("failed to get path number space: %v", err)
	}

	if _, isPending := pathSpace.PendingPackets[packetNumber]; !isPending {
		t.Fatalf("packet should be pending before marking as lost")
	}

	// Mark packet as lost
	err = manager.MarkPacketLost(pathID, packetNumber)
	if err != nil {
		t.Errorf("unexpected error marking packet as lost: %v", err)
		return
	}

	// Verify packet is no longer pending
	pathSpace, err = manager.GetPathNumberSpace(pathID)
	if err != nil {
		t.Fatalf("failed to get path number space after marking lost: %v", err)
	}

	if _, stillPending := pathSpace.PendingPackets[packetNumber]; stillPending {
		t.Errorf("packet should not be pending after marking as lost")
	}

	// Verify statistics were updated
	if pathSpace.TotalPacketsLost == 0 {
		t.Errorf("expected lost packet count to be updated")
	}
}

func TestPacketNumberingManager_GetPacketNumberingStatistics(t *testing.T) {
	manager := NewPacketNumberingManager(nil)

	// Register multiple paths
	paths := []string{"path-1", "path-2", "path-3"}
	for _, pathID := range paths {
		err := manager.RegisterPath(pathID)
		if err != nil {
			t.Fatalf("failed to register path %s: %v", pathID, err)
		}
	}

	// Record some packets on each path
	for _, pathID := range paths {
		for i := uint64(1); i <= 5; i++ {
			err := manager.RecordSentPacket(pathID, i, uint32(i), 1200, 2, false)
			if err != nil {
				t.Fatalf("failed to record packet on %s: %v", pathID, err)
			}
		}

		// ACK some packets
		ackFrame := &datapb.AckFrame{
			AckId:          1,
			AckedPacketIds: []uint64{1, 2, 3},
			PathId:         pathID,
			Timestamp:      uint64(time.Now().UnixNano()),
			LargestAcked:   3,
		}
		manager.ProcessAcknowledgment(pathID, ackFrame)

		// Mark one packet as lost
		manager.MarkPacketLost(pathID, 4)
	}

	// Get statistics
	stats := manager.GetPacketNumberingStatistics()

	// Verify overall statistics
	if stats.TotalPaths != len(paths) {
		t.Errorf("expected %d total paths, got %d", len(paths), stats.TotalPaths)
	}

	expectedTotalSent := uint64(len(paths) * 5) // 5 packets per path
	if stats.TotalPacketsSent != expectedTotalSent {
		t.Errorf("expected %d total packets sent, got %d", expectedTotalSent, stats.TotalPacketsSent)
	}

	expectedTotalAcked := uint64(len(paths) * 3) // 3 packets acked per path
	if stats.TotalPacketsAcked != expectedTotalAcked {
		t.Errorf("expected %d total packets acked, got %d", expectedTotalAcked, stats.TotalPacketsAcked)
	}

	expectedTotalLost := uint64(len(paths) * 1) // 1 packet lost per path
	if stats.TotalPacketsLost != expectedTotalLost {
		t.Errorf("expected %d total packets lost, got %d", expectedTotalLost, stats.TotalPacketsLost)
	}

	// Verify per-path statistics
	for _, pathID := range paths {
		pathStats, exists := stats.PathStatistics[pathID]
		if !exists {
			t.Errorf("missing statistics for path %s", pathID)
			continue
		}

		if pathStats.TotalPacketsSent != 5 {
			t.Errorf("expected 5 packets sent on %s, got %d", pathID, pathStats.TotalPacketsSent)
		}

		if pathStats.TotalPacketsAcked != 3 {
			t.Errorf("expected 3 packets acked on %s, got %d", pathID, pathStats.TotalPacketsAcked)
		}

		if pathStats.TotalPacketsLost != 1 {
			t.Errorf("expected 1 packet lost on %s, got %d", pathID, pathStats.TotalPacketsLost)
		}

		if pathStats.PendingPackets != 1 { // Packet 5 should still be pending
			t.Errorf("expected 1 pending packet on %s, got %d", pathID, pathStats.PendingPackets)
		}
	}
}

func TestPacketNumberingManager_UnregisterPath(t *testing.T) {
	manager := NewPacketNumberingManager(nil)

	pathID := "test-path"
	err := manager.RegisterPath(pathID)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	// Verify path exists
	_, err = manager.GetPathNumberSpace(pathID)
	if err != nil {
		t.Fatalf("path should exist after registration: %v", err)
	}

	// Unregister path
	err = manager.UnregisterPath(pathID)
	if err != nil {
		t.Errorf("unexpected error unregistering path: %v", err)
	}

	// Verify path no longer exists
	_, err = manager.GetPathNumberSpace(pathID)
	if err == nil {
		t.Errorf("path should not exist after unregistration")
	}

	// Test unregistering non-existent path
	err = manager.UnregisterPath("unknown-path")
	if err == nil {
		t.Errorf("expected error unregistering non-existent path")
	}
}

func BenchmarkPacketNumberingManager_AssignPacketNumber(b *testing.B) {
	manager := NewPacketNumberingManager(nil)
	pathID := "bench-path"
	manager.RegisterPath(pathID)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := manager.AssignPacketNumber(pathID)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

func BenchmarkPacketNumberingManager_RecordSentPacket(b *testing.B) {
	manager := NewPacketNumberingManager(nil)
	pathID := "bench-path"
	manager.RegisterPath(pathID)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := manager.RecordSentPacket(pathID, uint64(i+1), uint32(i+1), 1200, 2, false)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}