package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"kwik/internal/utils"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
)

// Performance and load tests for transport layer
// These tests verify Requirements 7.*, 8.*, 10.*

// BenchmarkPathCreation tests the performance of creating paths
func BenchmarkPathCreation(b *testing.B) {
	pm := NewPathManager()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		addr := fmt.Sprintf("127.0.0.1:808%d", i%10)
		conn := NewMockQuicConnection(addr, "127.0.0.1:0")

		_, err := pm.CreatePathFromConnection(conn)
		if err != nil {
			b.Fatalf("Failed to create path: %v", err)
		}
	}
}

// BenchmarkConcurrentPathCreation tests concurrent path creation performance
func BenchmarkConcurrentPathCreation(b *testing.B) {
	pm := NewPathManager()

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			addr := fmt.Sprintf("127.0.0.1:808%d", i%1000)
			conn := NewMockQuicConnection(addr, "127.0.0.1:0")

			_, err := pm.CreatePathFromConnection(conn)
			if err != nil {
				b.Fatalf("Failed to create path: %v", err)
			}
			i++
		}
	})
}

// BenchmarkPathLookup tests the performance of path lookup operations
func BenchmarkPathLookup(b *testing.B) {
	pathCounts := []int{10, 100, 1000, 10000}

	for _, pathCount := range pathCounts {
		b.Run(fmt.Sprintf("Paths_%d", pathCount), func(b *testing.B) {
			benchmarkPathLookup(b, pathCount)
		})
	}
}

func benchmarkPathLookup(b *testing.B, pathCount int) {
	pm := NewPathManager()

	// Pre-create paths
	pathIDs := make([]string, pathCount)
	for i := 0; i < pathCount; i++ {
		addr := fmt.Sprintf("127.0.0.1:808%d", i)
		conn := NewMockQuicConnection(addr, "127.0.0.1:0")

		path, err := pm.CreatePathFromConnection(conn)
		if err != nil {
			b.Fatalf("Failed to create path: %v", err)
		}
		pathIDs[i] = path.ID()
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pathID := pathIDs[i%pathCount]
		path := pm.GetPath(pathID)
		if path == nil {
			b.Fatalf("Path not found: %s", pathID)
		}
	}
}

// BenchmarkPathStateManagement tests path state change performance
func BenchmarkPathStateManagement(b *testing.B) {
	pm := NewPathManager()

	// Pre-create paths
	const pathCount = 100
	pathIDs := make([]string, pathCount)
	for i := 0; i < pathCount; i++ {
		addr := fmt.Sprintf("127.0.0.1:808%d", i)
		conn := NewMockQuicConnection(addr, "127.0.0.1:0")

		path, err := pm.CreatePathFromConnection(conn)
		if err != nil {
			b.Fatalf("Failed to create path: %v", err)
		}
		pathIDs[i] = path.ID()
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pathID := pathIDs[i%pathCount]

		// Alternate between marking dead and active
		if i%2 == 0 {
			err := pm.MarkPathDead(pathID)
			if err != nil {
				b.Fatalf("Failed to mark path dead: %v", err)
			}
		} else {
			// Re-activate path by creating new connection
			addr := fmt.Sprintf("127.0.0.1:808%d", i%pathCount)
			conn := NewMockQuicConnection(addr, "127.0.0.1:0")
			_, err := pm.CreatePathFromConnection(conn)
			if err != nil {
				b.Fatalf("Failed to reactivate path: %v", err)
			}
		}
	}
}

// BenchmarkPathHealthValidation tests health validation performance
func BenchmarkPathHealthValidation(b *testing.B) {
	pm := NewPathManager()

	// Pre-create paths with mixed health states
	const pathCount = 1000
	for i := 0; i < pathCount; i++ {
		addr := fmt.Sprintf("127.0.0.1:808%d", i)
		conn := NewMockQuicConnection(addr, "127.0.0.1:0")

		// Make some connections unhealthy
		if i%3 == 0 {
			conn.CloseWithError(0, "simulated failure")
		}

		_, err := pm.CreatePathFromConnection(conn)
		if err != nil {
			b.Fatalf("Failed to create path: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		unhealthyPaths := pm.ValidatePathHealth()
		// Expect about 1/3 of paths to be unhealthy
		expectedUnhealthy := pathCount / 3
		if len(unhealthyPaths) < expectedUnhealthy-50 || len(unhealthyPaths) > expectedUnhealthy+50 {
			b.Fatalf("Unexpected number of unhealthy paths: %d", len(unhealthyPaths))
		}
	}
}

// BenchmarkPathCleanup tests the performance of cleaning up dead paths
func BenchmarkPathCleanup(b *testing.B) {
	pm := NewPathManager()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create paths
		const pathsPerIteration = 10
		pathIDs := make([]string, pathsPerIteration)

		for j := 0; j < pathsPerIteration; j++ {
			addr := fmt.Sprintf("127.0.0.1:808%d", i*pathsPerIteration+j)
			conn := NewMockQuicConnection(addr, "127.0.0.1:0")

			path, err := pm.CreatePathFromConnection(conn)
			if err != nil {
				b.Fatalf("Failed to create path: %v", err)
			}
			pathIDs[j] = path.ID()
		}

		// Mark paths as dead
		for _, pathID := range pathIDs {
			err := pm.MarkPathDead(pathID)
			if err != nil {
				b.Fatalf("Failed to mark path dead: %v", err)
			}
		}

		// Clean up dead paths
		cleanedCount := pm.CleanupDeadPaths()
		if cleanedCount != pathsPerIteration {
			b.Fatalf("Expected to clean %d paths, cleaned %d", pathsPerIteration, cleanedCount)
		}
	}
}

// BenchmarkPathManagerScalability tests scalability with large numbers of paths
func BenchmarkPathManagerScalability(b *testing.B) {
	scales := []int{100, 1000, 5000, 10000}

	for _, scale := range scales {
		b.Run(fmt.Sprintf("Scale_%d", scale), func(b *testing.B) {
			benchmarkPathManagerScalability(b, scale)
		})
	}
}

func benchmarkPathManagerScalability(b *testing.B, scale int) {
	pm := NewPathManager()

	// Pre-populate with paths at the target scale
	pathIDs := make([]string, scale)
	for i := 0; i < scale; i++ {
		addr := fmt.Sprintf("127.0.0.1:%d", 8000+i)
		conn := NewMockQuicConnection(addr, "127.0.0.1:0")

		path, err := pm.CreatePathFromConnection(conn)
		if err != nil {
			b.Fatalf("Failed to create path: %v", err)
		}
		pathIDs[i] = path.ID()
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Benchmark operations at scale
	for i := 0; i < b.N; i++ {
		pathID := pathIDs[i%scale]

		// Mix of operations
		switch i % 4 {
		case 0:
			path := pm.GetPath(pathID)
			if path == nil {
				b.Fatalf("Path not found: %s", pathID)
			}
		case 1:
			_ = pm.GetActivePaths()
		case 2:
			_ = pm.GetPathCount()
		case 3:
			_ = pm.ValidatePathHealth()
		}
	}
}

// BenchmarkHealthMonitoring tests health monitoring performance
func BenchmarkHealthMonitoring(b *testing.B) {
	pm := NewPathManager()

	// Create paths
	const pathCount = 100
	for i := 0; i < pathCount; i++ {
		addr := fmt.Sprintf("127.0.0.1:808%d", i)
		conn := NewMockQuicConnection(addr, "127.0.0.1:0")

		_, err := pm.CreatePathFromConnection(conn)
		if err != nil {
			b.Fatalf("Failed to create path: %v", err)
		}
	}

	// Start health monitoring
	err := pm.StartHealthMonitoring()
	if err != nil {
		b.Fatalf("Failed to start health monitoring: %v", err)
	}
	defer pm.StopHealthMonitoring()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Simulate health check operations
		activePaths := pm.GetActivePaths()
		for _, path := range activePaths {
			metrics, err := pm.GetPathHealthMetrics(path.ID())
			if err != nil {
				b.Fatalf("Failed to get health metrics: %v", err)
			}
			_ = metrics
		}
	}
}

// BenchmarkConcurrentPathOperations tests concurrent path operations
func BenchmarkConcurrentPathOperations(b *testing.B) {
	pm := NewPathManager()

	// Pre-create some paths
	const initialPaths = 100
	pathIDs := make([]string, initialPaths)
	for i := 0; i < initialPaths; i++ {
		addr := fmt.Sprintf("127.0.0.1:808%d", i)
		conn := NewMockQuicConnection(addr, "127.0.0.1:0")

		path, err := pm.CreatePathFromConnection(conn)
		if err != nil {
			b.Fatalf("Failed to create path: %v", err)
		}
		pathIDs[i] = path.ID()
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 5 {
			case 0: // Create path
				addr := fmt.Sprintf("127.0.0.1:%d", 9000+i)
				conn := NewMockQuicConnection(addr, "127.0.0.1:0")
				_, err := pm.CreatePathFromConnection(conn)
				if err != nil {
					b.Errorf("Failed to create path: %v", err)
				}
			case 1: // Lookup path
				pathID := pathIDs[i%initialPaths]
				path := pm.GetPath(pathID)
				_ = path
			case 2: // Get active paths
				_ = pm.GetActivePaths()
			case 3: // Mark path dead
				if i%10 == 0 { // Only mark some paths dead
					pathID := pathIDs[i%initialPaths]
					pm.MarkPathDead(pathID)
				}
			case 4: // Validate health
				_ = pm.ValidatePathHealth()
			}
			i++
		}
	})
}

// BenchmarkPathIntegrityValidation tests integrity validation performance
func BenchmarkPathIntegrityValidation(b *testing.B) {
	pm := NewPathManager()

	// Create paths with some inconsistencies
	const pathCount = 1000
	for i := 0; i < pathCount; i++ {
		addr := fmt.Sprintf("127.0.0.1:808%d", i)
		conn := NewMockQuicConnection(addr, "127.0.0.1:0")

		_, err := pm.CreatePathFromConnection(conn)
		if err != nil {
			b.Fatalf("Failed to create path: %v", err)
		}
	}

	// Mark some paths as dead to create mixed state
	activePaths := pm.GetActivePaths()
	for i := 0; i < len(activePaths)/3; i++ {
		pm.MarkPathDead(activePaths[i].ID())
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		issues := pm.(*pathManager).ValidatePathIntegrity()
		// Should have no issues in a well-functioning system
		if len(issues) > 0 {
			b.Logf("Found %d integrity issues", len(issues))
		}
	}
}

// BenchmarkAutoCleanup tests automatic cleanup performance
func BenchmarkAutoCleanup(b *testing.B) {
	pm := NewPathManager()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create paths
		const pathsPerIteration = 20
		for j := 0; j < pathsPerIteration; j++ {
			addr := fmt.Sprintf("127.0.0.1:%d", 8000+i*pathsPerIteration+j)
			conn := NewMockQuicConnection(addr, "127.0.0.1:0")

			path, err := pm.CreatePathFromConnection(conn)
			if err != nil {
				b.Fatalf("Failed to create path: %v", err)
			}

			// Mark half as dead
			if j%2 == 0 {
				pm.MarkPathDead(path.ID())
			}
		}

		// Auto cleanup old dead paths
		cleanedCount := pm.(*pathManager).AutoCleanupDeadPaths(1 * time.Nanosecond) // Very short age for benchmarking

		// Should clean up about half the paths
		expectedCleaned := pathsPerIteration / 2
		if cleanedCount < expectedCleaned-2 || cleanedCount > expectedCleaned+2 {
			b.Fatalf("Expected to clean ~%d paths, cleaned %d", expectedCleaned, cleanedCount)
		}
	}
}

// BenchmarkPathStatistics tests statistics gathering performance
func BenchmarkPathStatistics(b *testing.B) {
	pm := NewPathManager()

	// Create paths with mixed states
	const pathCount = 1000
	for i := 0; i < pathCount; i++ {
		addr := fmt.Sprintf("127.0.0.1:808%d", i)
		conn := NewMockQuicConnection(addr, "127.0.0.1:0")

		path, err := pm.CreatePathFromConnection(conn)
		if err != nil {
			b.Fatalf("Failed to create path: %v", err)
		}

		// Mark some paths as dead
		if i%3 == 0 {
			pm.MarkPathDead(path.ID())
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		stats := pm.(*pathManager).GetPathStatistics()

		// Verify statistics make sense
		assert.Equal(b, pathCount, stats.TotalPaths)
		assert.Equal(b, pathCount*2/3, stats.ActivePaths)
		assert.Equal(b, pathCount/3, stats.DeadPaths)
		assert.NotEmpty(b, stats.PrimaryPathID)
	}
}

// Load test for sustained path operations
func BenchmarkSustainedPathLoad(b *testing.B) {
	pm := NewPathManager()

	// Start health monitoring
	err := pm.StartHealthMonitoring()
	if err != nil {
		b.Fatalf("Failed to start health monitoring: %v", err)
	}
	defer pm.StopHealthMonitoring()

	// Pre-create base paths
	const basePaths = 50
	basePathIDs := make([]string, basePaths)
	for i := 0; i < basePaths; i++ {
		addr := fmt.Sprintf("127.0.0.1:808%d", i)
		conn := NewMockQuicConnection(addr, "127.0.0.1:0")

		path, err := pm.CreatePathFromConnection(conn)
		if err != nil {
			b.Fatalf("Failed to create base path: %v", err)
		}
		basePathIDs[i] = path.ID()
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Simulate sustained load with mixed operations
	for i := 0; i < b.N; i++ {
		switch i % 8 {
		case 0: // Create new path
			addr := fmt.Sprintf("127.0.0.1:%d", 9000+i)
			conn := NewMockQuicConnection(addr, "127.0.0.1:0")
			_, err := pm.CreatePathFromConnection(conn)
			if err != nil {
				b.Fatalf("Failed to create path: %v", err)
			}
		case 1: // Lookup path
			pathID := basePathIDs[i%basePaths]
			path := pm.GetPath(pathID)
			_ = path
		case 2: // Get active paths
			_ = pm.GetActivePaths()
		case 3: // Get path count
			_ = pm.GetPathCount()
		case 4: // Mark path dead
			if i%20 == 0 { // Only occasionally mark paths dead
				pathID := basePathIDs[i%basePaths]
				pm.MarkPathDead(pathID)
			}
		case 5: // Validate health
			_ = pm.ValidatePathHealth()
		case 6: // Get statistics
			_ = pm.(*pathManager).GetPathStatistics()
		case 7: // Cleanup dead paths
			if i%100 == 0 { // Periodic cleanup
				pm.CleanupDeadPaths()
			}
		}
	}
}

// Enhanced MockQuicConnection for performance testing
type MockQuicConnection struct {
	remoteAddr string
	localAddr  string
	closed     bool
	ctx        context.Context
	cancel     context.CancelFunc
	mutex      sync.RWMutex
}

func NewMockQuicConnection(remoteAddr, localAddr string) *MockQuicConnection {
	ctx, cancel := context.WithCancel(context.Background())
	return &MockQuicConnection{
		remoteAddr: remoteAddr,
		localAddr:  localAddr,
		closed:     false,
		ctx:        ctx,
		cancel:     cancel,
	}
}

func (m *MockQuicConnection) RemoteAddr() net.Addr {
	return &MockNetAddr{addr: m.remoteAddr}
}

func (m *MockQuicConnection) LocalAddr() net.Addr {
	return &MockNetAddr{addr: m.localAddr}
}

func (m *MockQuicConnection) Context() context.Context {
	return m.ctx
}

func (m *MockQuicConnection) OpenStreamSync(ctx context.Context) (quic.Stream, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.closed {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "connection closed", nil)
	}

	return &MockQuicStream{}, nil
}

func (m *MockQuicConnection) CloseWithError(code quic.ApplicationErrorCode, reason string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.closed = true
	m.cancel()
	return nil
}

// Additional methods to satisfy quic.Connection interface
func (m *MockQuicConnection) AcceptStream(ctx context.Context) (quic.Stream, error) {
	return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "not implemented", nil)
}

func (m *MockQuicConnection) AcceptUniStream(ctx context.Context) (quic.ReceiveStream, error) {
	return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "not implemented", nil)
}

func (m *MockQuicConnection) OpenStream() (quic.Stream, error) {
	return m.OpenStreamSync(context.Background())
}

func (m *MockQuicConnection) OpenUniStream() (quic.SendStream, error) {
	return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "not implemented", nil)
}

func (m *MockQuicConnection) OpenUniStreamSync(ctx context.Context) (quic.SendStream, error) {
	return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "not implemented", nil)
}

func (m *MockQuicConnection) ConnectionState() quic.ConnectionState {
	return quic.ConnectionState{}
}

func (m *MockQuicConnection) SendDatagram(data []byte) error {
	return nil
}

func (m *MockQuicConnection) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "not implemented", nil)
}

// MockQuicStream for performance testing
type MockQuicStream struct{}

func (m *MockQuicStream) Read(p []byte) (int, error) {
	return 0, nil
}

func (m *MockQuicStream) Write(p []byte) (int, error) {
	return len(p), nil
}

func (m *MockQuicStream) Close() error {
	return nil
}

func (m *MockQuicStream) StreamID() quic.StreamID {
	return 0
}

func (m *MockQuicStream) SetDeadline(t time.Time) error {
	return nil
}

func (m *MockQuicStream) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *MockQuicStream) SetWriteDeadline(t time.Time) error {
	return nil
}

func (m *MockQuicStream) Context() context.Context {
	return context.Background()
}

func (m *MockQuicStream) CancelWrite(errorCode quic.StreamErrorCode) {}

func (m *MockQuicStream) CancelRead(errorCode quic.StreamErrorCode) {}

// MockNetAddr for performance testing
type MockNetAddr struct {
	addr string
}

func (m *MockNetAddr) String() string {
	return m.addr
}

func (m *MockNetAddr) Network() string {
	return "udp"
}
