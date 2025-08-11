package data

import (
	"testing"

	datapb "kwik/proto/data"
)

func TestPacketSizeCalculator_RegisterPath(t *testing.T) {
	calculator := NewPacketSizeCalculator(nil)

	tests := []struct {
		name         string
		pathID       string
		quicMaxSize  uint32
		mtu          uint32
		expectError  bool
	}{
		{
			name:        "valid path registration",
			pathID:      "path-1",
			quicMaxSize: 1200,
			mtu:         1500,
			expectError: false,
		},
		{
			name:        "path with default sizes",
			pathID:      "path-2",
			quicMaxSize: 0, // Should use default
			mtu:         0, // Should use default
			expectError: false,
		},
		{
			name:        "empty path ID",
			pathID:      "",
			quicMaxSize: 1200,
			mtu:         1500,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := calculator.RegisterPath(tt.pathID, tt.quicMaxSize, tt.mtu)

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
			config, err := calculator.GetPathSizeConfig(tt.pathID)
			if err != nil {
				t.Errorf("failed to get path config: %v", err)
				return
			}

			if config.PathID != tt.pathID {
				t.Errorf("expected path ID %s, got %s", tt.pathID, config.PathID)
			}

			// Check that effective max size was calculated
			if config.EffectiveMaxSize == 0 {
				t.Errorf("effective max size should not be zero")
			}

			// Effective max size should be less than QUIC max size due to overhead
			if config.EffectiveMaxSize >= config.QuicMaxPacketSize {
				t.Errorf("effective max size %d should be less than QUIC max size %d", 
					config.EffectiveMaxSize, config.QuicMaxPacketSize)
			}
		})
	}
}

func TestPacketSizeCalculator_CalculatePacketSize(t *testing.T) {
	calculator := NewPacketSizeCalculator(nil)

	// Register a test path
	pathID := "test-path"
	err := calculator.RegisterPath(pathID, 1200, 1500)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	tests := []struct {
		name        string
		pathID      string
		frames      []*datapb.DataFrame
		expectError bool
	}{
		{
			name:   "single small frame",
			pathID: pathID,
			frames: []*datapb.DataFrame{
				{
					FrameId:         1,
					LogicalStreamId: 1,
					Offset:          0,
					Data:            []byte("small data"),
					PathId:          pathID,
				},
			},
			expectError: false,
		},
		{
			name:   "multiple frames",
			pathID: pathID,
			frames: []*datapb.DataFrame{
				{
					FrameId:         1,
					LogicalStreamId: 1,
					Offset:          0,
					Data:            make([]byte, 100),
					PathId:          pathID,
				},
				{
					FrameId:         2,
					LogicalStreamId: 1,
					Offset:          100,
					Data:            make([]byte, 200),
					PathId:          pathID,
				},
			},
			expectError: false,
		},
		{
			name:   "large frame requiring fragmentation",
			pathID: pathID,
			frames: []*datapb.DataFrame{
				{
					FrameId:         1,
					LogicalStreamId: 1,
					Offset:          0,
					Data:            make([]byte, 2000), // Larger than typical max size
					PathId:          pathID,
				},
			},
			expectError: false,
		},
		{
			name:        "unregistered path",
			pathID:      "unknown-path",
			frames:      []*datapb.DataFrame{},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := calculator.CalculatePacketSize(tt.pathID, tt.frames)

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

			// Validate packet size info
			if info.PathID != tt.pathID {
				t.Errorf("expected path ID %s, got %s", tt.pathID, info.PathID)
			}

			if info.CalculatedSize == 0 {
				t.Errorf("calculated size should not be zero")
			}

			if info.CalculatedSize < info.EffectiveDataSize {
				t.Errorf("calculated size %d should be >= effective data size %d", 
					info.CalculatedSize, info.EffectiveDataSize)
			}

			// Check fragmentation logic
			if len(tt.frames) > 0 && len(tt.frames[0].Data) > 1500 {
				if !info.RequiresFragmentation {
					t.Errorf("large packet should require fragmentation")
				}
				if info.FragmentCount <= 1 {
					t.Errorf("fragment count should be > 1 for large packets")
				}
			}
		})
	}
}

func TestPacketSizeCalculator_CalculateMaxDataSize(t *testing.T) {
	calculator := NewPacketSizeCalculator(nil)

	// Register test paths with different configurations
	paths := []struct {
		pathID      string
		quicMaxSize uint32
		mtu         uint32
	}{
		{"small-path", 800, 1000},
		{"medium-path", 1200, 1500},
		{"large-path", 1400, 1500},
	}

	for _, path := range paths {
		err := calculator.RegisterPath(path.pathID, path.quicMaxSize, path.mtu)
		if err != nil {
			t.Fatalf("failed to register path %s: %v", path.pathID, err)
		}
	}

	for _, path := range paths {
		t.Run(path.pathID, func(t *testing.T) {
			maxSize, err := calculator.CalculateMaxDataSize(path.pathID)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if maxSize == 0 {
				t.Errorf("max data size should not be zero")
			}

			// Max data size should be less than QUIC max size due to overhead
			if maxSize >= path.quicMaxSize {
				t.Errorf("max data size %d should be less than QUIC max size %d", 
					maxSize, path.quicMaxSize)
			}
		})
	}

	// Test with unregistered path
	_, err := calculator.CalculateMaxDataSize("unknown-path")
	if err == nil {
		t.Errorf("expected error for unregistered path")
	}
}

func TestPacketSizeCalculator_ValidatePacketSize(t *testing.T) {
	calculator := NewPacketSizeCalculator(nil)

	pathID := "test-path"
	quicMaxSize := uint32(1200)
	err := calculator.RegisterPath(pathID, quicMaxSize, 1500)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	tests := []struct {
		name        string
		pathID      string
		packetSize  uint32
		expectError bool
	}{
		{
			name:        "valid packet size",
			pathID:      pathID,
			packetSize:  800,
			expectError: false,
		},
		{
			name:        "packet at QUIC limit",
			pathID:      pathID,
			packetSize:  quicMaxSize,
			expectError: false,
		},
		{
			name:        "packet exceeding QUIC limit",
			pathID:      pathID,
			packetSize:  quicMaxSize + 1,
			expectError: true,
		},
		{
			name:        "packet below minimum",
			pathID:      pathID,
			packetSize:  32, // Below typical minimum
			expectError: true,
		},
		{
			name:        "unregistered path",
			pathID:      "unknown-path",
			packetSize:  800,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := calculator.ValidatePacketSize(tt.pathID, tt.packetSize)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestPacketSizeCalculator_UpdatePathMTU(t *testing.T) {
	calculator := NewPacketSizeCalculator(nil)

	pathID := "test-path"
	initialMTU := uint32(1500)
	err := calculator.RegisterPath(pathID, 1200, initialMTU)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	// Get initial configuration
	initialConfig, err := calculator.GetPathSizeConfig(pathID)
	if err != nil {
		t.Fatalf("failed to get initial config: %v", err)
	}

	// Update MTU
	newMTU := uint32(9000) // Jumbo frame
	err = calculator.UpdatePathMTU(pathID, newMTU)
	if err != nil {
		t.Errorf("unexpected error updating MTU: %v", err)
		return
	}

	// Get updated configuration
	updatedConfig, err := calculator.GetPathSizeConfig(pathID)
	if err != nil {
		t.Fatalf("failed to get updated config: %v", err)
	}

	// Verify MTU was updated
	if updatedConfig.MTU != newMTU {
		t.Errorf("expected MTU %d, got %d", newMTU, updatedConfig.MTU)
	}

	// Verify effective max size was recalculated (may or may not change depending on implementation)
	// The effective max size depends on header overhead calculation which may change with MTU
	// For now, just verify it's still a reasonable value
	if updatedConfig.EffectiveMaxSize == 0 {
		t.Errorf("effective max size should not be zero after MTU update")
	}

	// Verify last updated timestamp changed
	if updatedConfig.LastUpdated <= initialConfig.LastUpdated {
		t.Errorf("last updated timestamp should have increased")
	}

	// Test with unregistered path
	err = calculator.UpdatePathMTU("unknown-path", newMTU)
	if err == nil {
		t.Errorf("expected error for unregistered path")
	}
}

func TestPacketSizeCalculator_GetOptimalFragmentSize(t *testing.T) {
	calculator := NewPacketSizeCalculator(nil)

	pathID := "test-path"
	err := calculator.RegisterPath(pathID, 1200, 1500)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	optimalSize, err := calculator.GetOptimalFragmentSize(pathID)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
		return
	}

	if optimalSize == 0 {
		t.Errorf("optimal fragment size should not be zero")
	}

	// Get the effective max size for comparison
	maxSize, err := calculator.CalculateMaxDataSize(pathID)
	if err != nil {
		t.Fatalf("failed to get max data size: %v", err)
	}

	// Optimal size should be less than max size (typically 80%)
	if optimalSize >= maxSize {
		t.Errorf("optimal fragment size %d should be less than max size %d", 
			optimalSize, maxSize)
	}

	// Test with unregistered path
	_, err = calculator.GetOptimalFragmentSize("unknown-path")
	if err == nil {
		t.Errorf("expected error for unregistered path")
	}
}

func TestPacketSizeCalculator_GetSizeStatistics(t *testing.T) {
	calculator := NewPacketSizeCalculator(nil)

	// Register multiple paths
	paths := []struct {
		pathID      string
		quicMaxSize uint32
		mtu         uint32
	}{
		{"path-1", 800, 1000},
		{"path-2", 1200, 1500},
		{"path-3", 1400, 1500},
	}

	for _, path := range paths {
		err := calculator.RegisterPath(path.pathID, path.quicMaxSize, path.mtu)
		if err != nil {
			t.Fatalf("failed to register path %s: %v", path.pathID, err)
		}
	}

	stats := calculator.GetSizeStatistics()

	// Verify basic statistics
	if stats.TotalPaths != len(paths) {
		t.Errorf("expected %d total paths, got %d", len(paths), stats.TotalPaths)
	}

	if stats.AverageEffectiveSize == 0 {
		t.Errorf("average effective size should not be zero")
	}

	if stats.MinEffectiveSize == 0 || stats.MaxEffectiveSize == 0 {
		t.Errorf("min/max effective sizes should not be zero")
	}

	if stats.MinEffectiveSize > stats.MaxEffectiveSize {
		t.Errorf("min effective size %d should be <= max effective size %d", 
			stats.MinEffectiveSize, stats.MaxEffectiveSize)
	}

	// Verify path-specific statistics
	if len(stats.PathStatistics) != len(paths) {
		t.Errorf("expected %d path statistics, got %d", len(paths), len(stats.PathStatistics))
	}

	for _, path := range paths {
		pathStats, exists := stats.PathStatistics[path.pathID]
		if !exists {
			t.Errorf("missing statistics for path %s", path.pathID)
			continue
		}

		if pathStats.PathID != path.pathID {
			t.Errorf("expected path ID %s, got %s", path.pathID, pathStats.PathID)
		}

		if pathStats.QuicMaxSize != path.quicMaxSize {
			t.Errorf("expected QUIC max size %d, got %d", path.quicMaxSize, pathStats.QuicMaxSize)
		}

		if pathStats.MTU != path.mtu {
			t.Errorf("expected MTU %d, got %d", path.mtu, pathStats.MTU)
		}
	}
}

func TestPacketSizeCalculator_UnregisterPath(t *testing.T) {
	calculator := NewPacketSizeCalculator(nil)

	pathID := "test-path"
	err := calculator.RegisterPath(pathID, 1200, 1500)
	if err != nil {
		t.Fatalf("failed to register path: %v", err)
	}

	// Verify path exists
	_, err = calculator.GetPathSizeConfig(pathID)
	if err != nil {
		t.Fatalf("path should exist after registration: %v", err)
	}

	// Unregister path
	err = calculator.UnregisterPath(pathID)
	if err != nil {
		t.Errorf("unexpected error unregistering path: %v", err)
	}

	// Verify path no longer exists
	_, err = calculator.GetPathSizeConfig(pathID)
	if err == nil {
		t.Errorf("path should not exist after unregistration")
	}

	// Test unregistering non-existent path
	err = calculator.UnregisterPath("unknown-path")
	if err == nil {
		t.Errorf("expected error unregistering non-existent path")
	}
}

func BenchmarkPacketSizeCalculator_CalculatePacketSize(b *testing.B) {
	calculator := NewPacketSizeCalculator(nil)
	pathID := "bench-path"
	calculator.RegisterPath(pathID, 1200, 1500)

	frames := []*datapb.DataFrame{
		{
			FrameId:         1,
			LogicalStreamId: 1,
			Offset:          0,
			Data:            make([]byte, 1000),
			PathId:          pathID,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := calculator.CalculatePacketSize(pathID, frames)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}