package session

import (
	"context"
	"testing"
	"time"

	"kwik/pkg/transport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// End-to-end compatibility tests demonstrating QUIC interface compatibility
// These tests verify Requirement 1.* - QUIC interface compatibility

// TestQUICCompatibility_BasicInterface tests that KWIK provides the same interface as QUIC
func TestQUICCompatibility_BasicInterface(t *testing.T) {
	t.Log("Starting TestQUICCompatibility_BasicInterface")

	// Create a KWIK session
	t.Log("Creating path manager...")
	pathManager := transport.NewPathManager()
	t.Log("Path manager created successfully")

	t.Log("Creating session config...")
	config := DefaultSessionConfig()
	t.Log("Session config created successfully")

	t.Log("Creating client session...")
	session := NewClientSession(pathManager, config)
	t.Log("Client session created successfully")

	// Set up a mock primary path
	t.Log("Creating mock QUIC connection...")
	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")

	// The mock connection will create streams dynamically with proper closed state handling
	// No need to pre-configure streams since NewMockQuicConnection handles this

	t.Log("Mock QUIC connection created successfully")

	t.Log("Creating primary path from connection...")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)
	t.Logf("Primary path created successfully with ID: %s", primaryPath.ID())

	t.Log("Setting up session with primary path...")
	session.primaryPath = primaryPath
	session.state = SessionStateActive
	t.Log("Session setup completed")

	// Test that KWIK Session interface matches QUIC expectations

	// 1. Test OpenStreamSync - should work like QUIC
	t.Log("Testing OpenStreamSync...")
	ctx := context.Background()
	stream, err := session.OpenStreamSync(ctx)
	t.Logf("OpenStreamSync completed with error: %v", err)
	assert.NoError(t, err)
	assert.NotNil(t, stream)
	t.Log("OpenStreamSync test passed")

	// Verify stream has QUIC-compatible interface
	t.Log("Testing stream interface compliance...")
	assert.Implements(t, (*Stream)(nil), stream)
	t.Log("Stream interface compliance test passed")

	// 2. Test OpenStream - should work like QUIC
	t.Log("Testing OpenStream...")
	stream2, err := session.OpenStream()
	t.Logf("OpenStream completed with error: %v", err)
	assert.NoError(t, err)
	assert.NotNil(t, stream2)
	t.Log("OpenStream test passed")

	// 3. Test that streams have unique IDs (QUIC behavior)
	t.Log("Testing stream ID uniqueness...")
	t.Log("Getting stream1 ID...")
	stream1ID := stream.StreamID()
	t.Logf("Stream1 ID: %d", stream1ID)

	t.Log("Getting stream2 ID...")
	stream2ID := stream2.StreamID()
	t.Logf("Stream2 ID: %d", stream2ID)

	t.Log("Comparing stream IDs...")
	assert.NotEqual(t, stream1ID, stream2ID)
	t.Log("Stream ID uniqueness test passed")

	// 4. Test stream Read/Write interface compatibility
	t.Log("Testing stream Write interface...")
	testData := []byte("QUIC compatibility test")
	t.Logf("Writing %d bytes to stream...", len(testData))
	n, err := stream.Write(testData)
	t.Logf("Write completed: wrote %d bytes, error: %v", n, err)
	assert.NoError(t, err)
	assert.Equal(t, len(testData), n)
	t.Log("Stream Write test passed")

	// 5. Test stream Close - should work like QUIC
	t.Log("Testing stream Close...")
	err = stream.Close()
	t.Logf("Stream Close completed with error: %v", err)
	assert.NoError(t, err)
	t.Log("Stream Close test passed")

	// 6. Test session Close - should work like QUIC
	t.Log("Testing session Close...")
	err = session.Close()
	t.Logf("Session Close completed with error: %v", err)
	assert.NoError(t, err)
	t.Log("Session Close test passed")

	t.Log("TestQUICCompatibility_BasicInterface completed successfully")
}

// TestQUICCompatibility_StreamBehavior tests that KWIK streams behave like QUIC streams
func TestQUICCompatibility_StreamBehavior(t *testing.T) {
	// Set up KWIK session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	// Create stream
	stream, err := session.OpenStreamSync(context.Background())
	require.NoError(t, err)

	// Test QUIC-like stream behavior

	// 1. Test Write returns correct byte count (QUIC behavior)
	testData := []byte("Hello, QUIC compatibility!")
	n, err := stream.Write(testData)
	assert.NoError(t, err)
	assert.Equal(t, len(testData), n)

	// 2. Test multiple writes (QUIC behavior)
	testData2 := []byte(" Additional data.")
	n2, err := stream.Write(testData2)
	assert.NoError(t, err)
	assert.Equal(t, len(testData2), n2)

	// 3. Test empty write (QUIC behavior)
	n3, err := stream.Write([]byte{})
	assert.NoError(t, err)
	assert.Equal(t, 0, n3)

	// 4. Test stream metadata (KWIK-specific but compatible)
	assert.NotZero(t, stream.StreamID())
	assert.NotEmpty(t, stream.PathID())

	// 5. Test stream close (QUIC behavior)
	err = stream.Close()
	assert.NoError(t, err)

	// 6. Test operations after close fail (QUIC behavior)
	_, err = stream.Write([]byte("should fail"))
	assert.Error(t, err)
}

// TestQUICCompatibility_ErrorHandling tests that KWIK error handling matches QUIC
func TestQUICCompatibility_ErrorHandling(t *testing.T) {
	// Set up KWIK session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	// Test operations on inactive session (QUIC-like error behavior)
	session.state = SessionStateClosed

	// 1. Test OpenStreamSync fails on closed session (QUIC behavior)
	stream, err := session.OpenStreamSync(context.Background())
	assert.Error(t, err)
	assert.Nil(t, stream)
	assert.Contains(t, err.Error(), "session is not active")

	// 2. Test OpenStream fails on closed session (QUIC behavior)
	stream2, err := session.OpenStream()
	assert.Error(t, err)
	assert.Nil(t, stream2)

	// 3. Test AcceptStream with timeout (QUIC behavior)
	session.state = SessionStateActive
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	stream3, err := session.AcceptStream(ctx)
	assert.Error(t, err)
	assert.Nil(t, stream3)
	assert.Equal(t, context.DeadlineExceeded, err)
}

// TestQUICCompatibility_SessionLifecycle tests QUIC-compatible session lifecycle
func TestQUICCompatibility_SessionLifecycle(t *testing.T) {
	// Test session creation (QUIC-like)
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	// 1. Test initial session state
	assert.NotEmpty(t, session.GetSessionID())
	assert.Equal(t, SessionStateConnecting, session.state)

	// 2. Test session activation
	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	// 3. Test session operations work when active
	stream, err := session.OpenStreamSync(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, stream)

	// 4. Test session close (QUIC-like)
	err = session.Close()
	assert.NoError(t, err)
	assert.Equal(t, SessionStateClosed, session.state)

	// 5. Test operations fail after close (QUIC behavior)
	stream2, err := session.OpenStreamSync(context.Background())
	assert.Error(t, err)
	assert.Nil(t, stream2)

	// 6. Test double close is safe (QUIC behavior)
	err = session.Close()
	assert.NoError(t, err)
}

// TestQUICCompatibility_ConcurrentOperations tests QUIC-like concurrent behavior
func TestQUICCompatibility_ConcurrentOperations(t *testing.T) {
	// Set up KWIK session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	// Test concurrent stream creation (QUIC-like)
	const numStreams = 10
	streamChan := make(chan Stream, numStreams)
	errorChan := make(chan error, numStreams)

	// Create streams concurrently
	for i := 0; i < numStreams; i++ {
		go func() {
			stream, err := session.OpenStreamSync(context.Background())
			if err != nil {
				errorChan <- err
			} else {
				streamChan <- stream
			}
		}()
	}

	// Collect results
	var streams []Stream
	var errors []error

	for i := 0; i < numStreams; i++ {
		select {
		case stream := <-streamChan:
			streams = append(streams, stream)
		case err := <-errorChan:
			errors = append(errors, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent stream creation")
		}
	}

	// Verify QUIC-like behavior
	assert.Len(t, errors, 0, "No errors should occur during concurrent stream creation")
	assert.Len(t, streams, numStreams, "All streams should be created successfully")

	// Verify all streams have unique IDs (QUIC behavior)
	streamIDs := make(map[uint64]bool)
	for _, stream := range streams {
		assert.False(t, streamIDs[stream.StreamID()], "Stream ID should be unique: %d", stream.StreamID())
		streamIDs[stream.StreamID()] = true
	}
}

// TestQUICCompatibility_DataTransfer tests QUIC-compatible data transfer
func TestQUICCompatibility_DataTransfer(t *testing.T) {
	// Set up KWIK session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	// Create stream
	stream, err := session.OpenStreamSync(context.Background())
	require.NoError(t, err)

	// Test QUIC-like data transfer patterns

	// 1. Test small write (QUIC behavior)
	smallData := []byte("small")
	n, err := stream.Write(smallData)
	assert.NoError(t, err)
	assert.Equal(t, len(smallData), n)

	// 2. Test large write (QUIC behavior)
	largeData := make([]byte, 10*1024) // 10KB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	n, err = stream.Write(largeData)
	assert.NoError(t, err)
	assert.Equal(t, len(largeData), n)

	// 3. Test multiple sequential writes (QUIC behavior)
	for i := 0; i < 10; i++ {
		data := []byte("sequential write " + string(rune('0'+i)))
		n, err := stream.Write(data)
		assert.NoError(t, err)
		assert.Equal(t, len(data), n)
	}

	// 4. Test read behavior (QUIC-like, though simplified in mock)
	readBuf := make([]byte, 100)
	n, err = stream.Read(readBuf)
	// In our mock implementation, this returns 0 bytes (no data available)
	assert.NoError(t, err)
	assert.Equal(t, 0, n)
}

// TestMigrationFromQUIC_InterfaceCompatibility tests migration scenarios
func TestMigrationFromQUIC_InterfaceCompatibility(t *testing.T) {
	// This test demonstrates how existing QUIC code can work with KWIK

	// Simulate existing QUIC code that expects certain interfaces
	var quicLikeSession interface {
		OpenStreamSync(ctx context.Context) (Stream, error)
		OpenStream() (Stream, error)
		Close() error
	}

	// Create KWIK session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	// Assign KWIK session to QUIC-like interface (should work seamlessly)
	quicLikeSession = session

	// Test that existing QUIC code patterns work with KWIK

	// 1. Test OpenStreamSync (existing QUIC pattern)
	stream, err := quicLikeSession.OpenStreamSync(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, stream)

	// 2. Test OpenStream (existing QUIC pattern)
	stream2, err := quicLikeSession.OpenStream()
	assert.NoError(t, err)
	assert.NotNil(t, stream2)

	// 3. Test Close (existing QUIC pattern)
	err = quicLikeSession.Close()
	assert.NoError(t, err)

	// This demonstrates that KWIK can be a drop-in replacement for QUIC
}

// TestMigrationFromQUIC_StreamCompatibility tests stream interface migration
func TestMigrationFromQUIC_StreamCompatibility(t *testing.T) {
	// Simulate existing QUIC stream interface expectations
	var quicLikeStream interface {
		Read([]byte) (int, error)
		Write([]byte) (int, error)
		Close() error
	}

	// Create KWIK session and stream
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	stream, err := session.OpenStreamSync(context.Background())
	require.NoError(t, err)

	// Assign KWIK stream to QUIC-like interface
	quicLikeStream = stream

	// Test existing QUIC stream patterns work with KWIK

	// 1. Test Write (existing QUIC pattern)
	testData := []byte("migration test data")
	n, err := quicLikeStream.Write(testData)
	assert.NoError(t, err)
	assert.Equal(t, len(testData), n)

	// 2. Test Read (existing QUIC pattern)
	readBuf := make([]byte, 100)
	n, err = quicLikeStream.Read(readBuf)
	assert.NoError(t, err)
	// Mock returns 0 bytes, which is valid QUIC behavior for no data available

	// 3. Test Close (existing QUIC pattern)
	err = quicLikeStream.Close()
	assert.NoError(t, err)
}

// TestQUICCompatibility_ContextHandling tests context handling like QUIC
func TestQUICCompatibility_ContextHandling(t *testing.T) {
	// Set up KWIK session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	// Test context cancellation (QUIC-like behavior)
	ctx, cancel := context.WithCancel(context.Background())

	// Start operation
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel() // Cancel context
	}()

	// Test that context cancellation is respected (QUIC behavior)
	stream, err := session.AcceptStream(ctx)
	assert.Error(t, err)
	assert.Nil(t, stream)
	assert.Equal(t, context.Canceled, err)

	// Test timeout context (QUIC-like behavior)
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer timeoutCancel()

	stream2, err := session.AcceptStream(timeoutCtx)
	assert.Error(t, err)
	assert.Nil(t, stream2)
	assert.Equal(t, context.DeadlineExceeded, err)
}

// TestQUICCompatibility_ResourceCleanup tests QUIC-like resource cleanup
func TestQUICCompatibility_ResourceCleanup(t *testing.T) {
	// Set up KWIK session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	// Create multiple streams
	const numStreams = 5
	streams := make([]Stream, numStreams)
	for i := 0; i < numStreams; i++ {
		stream, err := session.OpenStreamSync(context.Background())
		require.NoError(t, err)
		streams[i] = stream
	}

	// Verify streams are tracked
	assert.Equal(t, numStreams, len(session.streams))

	// Close individual streams (QUIC behavior)
	for i, stream := range streams {
		err := stream.Close()
		assert.NoError(t, err, "Stream %d should close without error", i)
	}

	// Verify streams are cleaned up
	assert.Equal(t, 0, len(session.streams))

	// Test session cleanup (QUIC behavior)
	err = session.Close()
	assert.NoError(t, err)

	// Verify session state
	assert.Equal(t, SessionStateClosed, session.state)
}

// TestQUICCompatibility_EdgeCases tests edge cases that QUIC handles
func TestQUICCompatibility_EdgeCases(t *testing.T) {
	// Set up KWIK session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	// Create stream for testing
	stream, err := session.OpenStreamSync(context.Background())
	require.NoError(t, err)

	// Test edge cases that QUIC handles

	// 1. Test nil buffer write (should handle gracefully like QUIC)
	n, err := stream.Write(nil)
	assert.NoError(t, err)
	assert.Equal(t, 0, n)

	// 2. Test empty buffer write (QUIC behavior)
	n, err = stream.Write([]byte{})
	assert.NoError(t, err)
	assert.Equal(t, 0, n)

	// 3. Test nil buffer read (should handle gracefully like QUIC)
	n, err = stream.Read(nil)
	assert.NoError(t, err)
	assert.Equal(t, 0, n)

	// 4. Test empty buffer read (QUIC behavior)
	n, err = stream.Read([]byte{})
	assert.NoError(t, err)
	assert.Equal(t, 0, n)

	// 5. Test operations on closed stream (QUIC behavior)
	err = stream.Close()
	assert.NoError(t, err)

	_, err = stream.Write([]byte("should fail"))
	assert.Error(t, err)

	_, err = stream.Read(make([]byte, 10))
	assert.Error(t, err)
}

// TestQUICCompatibility_PerformanceCharacteristics tests that KWIK maintains QUIC-like performance
func TestQUICCompatibility_PerformanceCharacteristics(t *testing.T) {
	// Set up KWIK session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	// Test that KWIK operations complete in reasonable time (QUIC-like performance)

	// 1. Test stream creation performance
	start := time.Now()
	const numStreams = 100

	for i := 0; i < numStreams; i++ {
		stream, err := session.OpenStreamSync(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, stream)
	}

	duration := time.Since(start)

	// Should complete quickly (QUIC-like performance expectation)
	assert.Less(t, duration.Nanoseconds(), (1 * time.Second).Nanoseconds(), "Stream creation should be fast")

	// 2. Test write performance
	stream, err := session.OpenStreamSync(context.Background())
	require.NoError(t, err)

	testData := make([]byte, 1024) // 1KB
	start = time.Now()

	const numWrites = 1000
	for i := 0; i < numWrites; i++ {
		_, err := stream.Write(testData)
		assert.NoError(t, err)
	}

	writeDuration := time.Since(start)

	// Should complete quickly (QUIC-like performance expectation)
	assert.Less(t, writeDuration.Nanoseconds(), (1 * time.Second).Nanoseconds(), "Writes should be fast")

	// Calculate throughput (should be reasonable for QUIC compatibility)
	totalBytes := numWrites * len(testData)
	throughputMBps := float64(totalBytes) / writeDuration.Seconds() / (1024 * 1024)

	// Should achieve reasonable throughput (this is a mock, so it should be very high)
	assert.Greater(t, throughputMBps, 1.0, "Should achieve reasonable throughput")
}

// TestQUICCompatibility_ErrorTypes tests that KWIK returns QUIC-compatible error types
func TestQUICCompatibility_ErrorTypes(t *testing.T) {
	// Set up KWIK session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	// Test error types match QUIC expectations

	// 1. Test session not ready error
	session.state = SessionStateClosed
	_, err := session.OpenStreamSync(context.Background())
	assert.Error(t, err)
	// Error should be descriptive and indicate session state issue
	assert.Contains(t, err.Error(), "session is not active")

	// 2. Test path-related errors
	session.state = SessionStateActive
	session.primaryPath = nil
	_, err = session.OpenStreamSync(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no primary path available")

	// 3. Test context cancellation errors
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err = session.AcceptStream(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)

	// 4. Test timeout errors
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer timeoutCancel()
	time.Sleep(1 * time.Millisecond) // Ensure timeout

	_, err = session.AcceptStream(timeoutCtx)
	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
}

// TestQUICCompatibility_InterfaceCompliance tests full interface compliance
func TestQUICCompatibility_InterfaceCompliance(t *testing.T) {
	// This test ensures KWIK implements all expected QUIC interfaces correctly

	// Test Session interface compliance
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	session := NewClientSession(pathManager, config)

	// Verify Session implements expected interface
	var _ interface {
		OpenStreamSync(ctx context.Context) (Stream, error)
		OpenStream() (Stream, error)
		AcceptStream(ctx context.Context) (Stream, error)
		Close() error
	} = session

	// Set up session for stream testing
	mockConn := NewMockQuicConnection("127.0.0.1:8080", "127.0.0.1:0")
	primaryPath, err := pathManager.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	session.primaryPath = primaryPath
	session.state = SessionStateActive

	// Test Stream interface compliance
	stream, err := session.OpenStreamSync(context.Background())
	require.NoError(t, err)

	// Verify Stream implements expected interface
	var _ interface {
		Read([]byte) (int, error)
		Write([]byte) (int, error)
		Close() error
	} = stream

	// Verify KWIK-specific extensions don't break compatibility
	var _ interface {
		StreamID() uint64
		PathID() string
	} = stream

	// Test that all methods work as expected
	testData := []byte("interface compliance test")
	n, err := stream.Write(testData)
	assert.NoError(t, err)
	assert.Equal(t, len(testData), n)

	readBuf := make([]byte, 100)
	n, err = stream.Read(readBuf)
	assert.NoError(t, err)

	err = stream.Close()
	assert.NoError(t, err)

	err = session.Close()
	assert.NoError(t, err)
}
