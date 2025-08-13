package stream

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test helper functions
func createTestMetadataProtocol() *MetadataProtocolImpl {
	return NewMetadataProtocol()
}

func createTestMetadata(kwikStreamID, offset uint64, dataLength uint32, pathID string, msgType MetadataType) *StreamMetadata {
	return &StreamMetadata{
		KwikStreamID: kwikStreamID,
		Offset:       offset,
		DataLength:   dataLength,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathID: pathID,
		MessageType:  msgType,
	}
}

// Tests for NewMetadataProtocol
func TestNewMetadataProtocol(t *testing.T) {
	protocol := NewMetadataProtocol()
	
	assert.NotNil(t, protocol)
	assert.Equal(t, uint32(1024*1024), protocol.maxDataLength)
	assert.Equal(t, 255, protocol.maxPathIDLen)
}

// Tests for MetadataType.String()
func TestMetadataType_String(t *testing.T) {
	tests := []struct {
		msgType  MetadataType
		expected string
	}{
		{MetadataTypeData, "DATA"},
		{MetadataTypeStreamOpen, "STREAM_OPEN"},
		{MetadataTypeStreamClose, "STREAM_CLOSE"},
		{MetadataTypeOffsetSync, "OFFSET_SYNC"},
		{MetadataType(999), "UNKNOWN"},
	}
	
	for _, test := range tests {
		t.Run(test.expected, func(t *testing.T) {
			assert.Equal(t, test.expected, test.msgType.String())
		})
	}
}

// Tests for EncapsulateData
func TestEncapsulateData(t *testing.T) {
	t.Run("successfully encapsulates data", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		kwikStreamID := uint64(100)
		offset := uint64(1024)
		data := []byte("hello world")
		
		encapsulated, err := protocol.EncapsulateData(kwikStreamID, offset, data)
		
		assert.NoError(t, err)
		assert.NotNil(t, encapsulated)
		assert.True(t, len(encapsulated) > len(data)) // Should be larger due to metadata
		
		// Verify we can decode it back
		metadata, decodedData, err := protocol.DecapsulateData(encapsulated)
		assert.NoError(t, err)
		assert.Equal(t, kwikStreamID, metadata.KwikStreamID)
		assert.Equal(t, offset, metadata.Offset)
		assert.Equal(t, uint32(len(data)), metadata.DataLength)
		assert.Equal(t, MetadataTypeData, metadata.MessageType)
		assert.Equal(t, data, decodedData)
	})
	
	t.Run("handles empty data", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		encapsulated, err := protocol.EncapsulateData(100, 0, []byte{})
		
		assert.NoError(t, err)
		assert.NotNil(t, encapsulated)
		
		// Verify we can decode it back
		metadata, decodedData, err := protocol.DecapsulateData(encapsulated)
		assert.NoError(t, err)
		assert.Equal(t, uint32(0), metadata.DataLength)
		assert.Empty(t, decodedData)
	})
	
	t.Run("handles large data", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create large data (but within limits)
		largeData := make([]byte, 100000)
		for i := range largeData {
			largeData[i] = byte(i % 256)
		}
		
		encapsulated, err := protocol.EncapsulateData(100, 0, largeData)
		
		assert.NoError(t, err)
		assert.NotNil(t, encapsulated)
		
		// Verify we can decode it back
		metadata, decodedData, err := protocol.DecapsulateData(encapsulated)
		assert.NoError(t, err)
		assert.Equal(t, uint32(len(largeData)), metadata.DataLength)
		assert.Equal(t, largeData, decodedData)
	})
	
	t.Run("rejects data that is too large", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create data larger than the limit
		tooLargeData := make([]byte, protocol.maxDataLength+1)
		
		_, err := protocol.EncapsulateData(100, 0, tooLargeData)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataDataTooLarge, protocolErr.Code)
	})
	
	t.Run("sets correct timestamp", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		beforeTime := time.Now().UnixNano()
		encapsulated, err := protocol.EncapsulateData(100, 0, []byte("test"))
		afterTime := time.Now().UnixNano()
		
		require.NoError(t, err)
		
		metadata, _, err := protocol.DecapsulateData(encapsulated)
		require.NoError(t, err)
		
		assert.True(t, int64(metadata.Timestamp) >= beforeTime)
		assert.True(t, int64(metadata.Timestamp) <= afterTime)
	})
}

// Tests for DecapsulateData
func TestDecapsulateData(t *testing.T) {
	t.Run("successfully decapsulates valid frame", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create a valid frame first
		originalData := []byte("test data")
		encapsulated, err := protocol.EncapsulateData(100, 1024, originalData)
		require.NoError(t, err)
		
		// Decapsulate it
		metadata, data, err := protocol.DecapsulateData(encapsulated)
		
		assert.NoError(t, err)
		assert.NotNil(t, metadata)
		assert.Equal(t, uint64(100), metadata.KwikStreamID)
		assert.Equal(t, uint64(1024), metadata.Offset)
		assert.Equal(t, uint32(len(originalData)), metadata.DataLength)
		assert.Equal(t, MetadataTypeData, metadata.MessageType)
		assert.Equal(t, originalData, data)
	})
	
	t.Run("rejects frame that is too small", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		tooSmallFrame := make([]byte, minMetadataFrameSize-1)
		
		_, _, err := protocol.DecapsulateData(tooSmallFrame)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataFrameTooSmall, protocolErr.Code)
	})
	
	t.Run("rejects frame with invalid magic", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create a frame with invalid magic
		frame := make([]byte, minMetadataFrameSize)
		binary.BigEndian.PutUint32(frame[0:], 0xDEADBEEF) // Invalid magic
		
		_, _, err := protocol.DecapsulateData(frame)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataInvalidMagic, protocolErr.Code)
	})
	
	t.Run("rejects frame with unsupported version", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create a frame with unsupported version
		frame := make([]byte, minMetadataFrameSize)
		binary.BigEndian.PutUint32(frame[0:], metadataFrameMagic) // Valid magic
		frame[4] = 99                                             // Invalid version
		
		_, _, err := protocol.DecapsulateData(frame)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataUnsupportedVersion, protocolErr.Code)
	})
	
	t.Run("handles frame with path ID", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create metadata with path ID
		metadata := createTestMetadata(100, 1024, 5, "path1", MetadataTypeData)
		data := []byte("hello")
		
		frame, err := protocol.encodeFrame(metadata, data)
		require.NoError(t, err)
		
		// Decapsulate it
		decodedMetadata, decodedData, err := protocol.DecapsulateData(frame)
		
		assert.NoError(t, err)
		assert.Equal(t, "path1", decodedMetadata.SourcePathID)
		assert.Equal(t, data, decodedData)
	})
	
	t.Run("handles corrupted frame - path ID extends beyond frame", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create a frame where path ID length is larger than remaining data
		frame := make([]byte, minMetadataFrameSize)
		binary.BigEndian.PutUint32(frame[0:], metadataFrameMagic)
		frame[4] = metadataFrameVersion
		frame[5] = byte(MetadataTypeData)
		// Set path ID length to a value larger than remaining frame
		frame[minMetadataFrameSize-1] = 100
		
		_, _, err := protocol.DecapsulateData(frame)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataFrameCorrupted, protocolErr.Code)
		assert.Contains(t, protocolErr.Message, "path ID extends beyond frame")
	})
	
	t.Run("handles corrupted frame - data extends beyond frame", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create a frame where data length is larger than remaining data
		frame := make([]byte, minMetadataFrameSize+5) // Add some space for path ID
		offset := 0
		
		// Header
		binary.BigEndian.PutUint32(frame[offset:], metadataFrameMagic)
		offset += 4
		frame[offset] = metadataFrameVersion
		offset++
		frame[offset] = byte(MetadataTypeData)
		offset++
		offset += 2 // Reserved
		
		// Fixed fields
		binary.BigEndian.PutUint64(frame[offset:], 100) // KWIK stream ID
		offset += 8
		binary.BigEndian.PutUint64(frame[offset:], 0) // Offset
		offset += 8
		binary.BigEndian.PutUint32(frame[offset:], 1000) // Data length (too large)
		offset += 4
		binary.BigEndian.PutUint64(frame[offset:], uint64(time.Now().UnixNano())) // Timestamp
		offset += 8
		
		// Path ID
		frame[offset] = 0 // Empty path ID
		
		_, _, err := protocol.DecapsulateData(frame)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataFrameCorrupted, protocolErr.Code)
		assert.Contains(t, protocolErr.Message, "data extends beyond frame")
	})
}

// Tests for ValidateMetadata
func TestValidateMetadata(t *testing.T) {
	t.Run("validates correct metadata", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		metadata := createTestMetadata(100, 1024, 10, "path1", MetadataTypeData)
		
		err := protocol.ValidateMetadata(metadata)
		assert.NoError(t, err)
	})
	
	t.Run("rejects nil metadata", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		err := protocol.ValidateMetadata(nil)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataInvalid, protocolErr.Code)
		assert.Contains(t, protocolErr.Message, "metadata is nil")
	})
	
	t.Run("rejects zero KWIK stream ID", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		metadata := createTestMetadata(0, 1024, 10, "path1", MetadataTypeData)
		
		err := protocol.ValidateMetadata(metadata)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataInvalid, protocolErr.Code)
		assert.Contains(t, protocolErr.Message, "KWIK stream ID cannot be zero")
	})
	
	t.Run("rejects data length that is too large", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		metadata := createTestMetadata(100, 1024, protocol.maxDataLength+1, "path1", MetadataTypeData)
		
		err := protocol.ValidateMetadata(metadata)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataDataTooLarge, protocolErr.Code)
	})
	
	t.Run("rejects path ID that is too long", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		longPathID := string(make([]byte, protocol.maxPathIDLen+1))
		metadata := createTestMetadata(100, 1024, 10, longPathID, MetadataTypeData)
		
		err := protocol.ValidateMetadata(metadata)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataInvalid, protocolErr.Code)
		assert.Contains(t, protocolErr.Message, "path ID length")
	})
	
	t.Run("rejects invalid message type", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		metadata := createTestMetadata(100, 1024, 10, "path1", MetadataType(999))
		
		err := protocol.ValidateMetadata(metadata)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataInvalid, protocolErr.Code)
		assert.Contains(t, protocolErr.Message, "invalid message type")
	})
	
	t.Run("accepts all valid message types", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		validTypes := []MetadataType{
			MetadataTypeData,
			MetadataTypeStreamOpen,
			MetadataTypeStreamClose,
			MetadataTypeOffsetSync,
		}
		
		for _, msgType := range validTypes {
			metadata := createTestMetadata(100, 1024, 10, "path1", msgType)
			err := protocol.ValidateMetadata(metadata)
			assert.NoError(t, err, "Message type %s should be valid", msgType.String())
		}
	})
}

// Tests for CreateStreamOpenMetadata
func TestCreateStreamOpenMetadata(t *testing.T) {
	t.Run("creates valid stream open metadata", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		kwikStreamID := uint64(100)
		
		metadata, err := protocol.CreateStreamOpenMetadata(kwikStreamID)
		
		assert.NoError(t, err)
		assert.NotNil(t, metadata)
		assert.Equal(t, kwikStreamID, metadata.KwikStreamID)
		assert.Equal(t, uint64(0), metadata.Offset)
		assert.Equal(t, uint32(0), metadata.DataLength)
		assert.Equal(t, MetadataTypeStreamOpen, metadata.MessageType)
		assert.Equal(t, "", metadata.SourcePathID)
		assert.True(t, metadata.Timestamp > 0)
	})
	
	t.Run("rejects zero KWIK stream ID", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		_, err := protocol.CreateStreamOpenMetadata(0)
		
		assert.Error(t, err)
		protocolErr, ok := err.(*MetadataProtocolError)
		assert.True(t, ok, "Expected MetadataProtocolError")
		assert.Equal(t, ErrMetadataInvalid, protocolErr.Code)
	})
}

// Tests for round-trip encoding/decoding
func TestRoundTripEncodingDecoding(t *testing.T) {
	t.Run("round trip with various data sizes", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		testCases := []struct {
			name string
			data []byte
		}{
			{"empty", []byte{}},
			{"small", []byte("hello")},
			{"medium", bytes.Repeat([]byte("test"), 100)},
			{"large", bytes.Repeat([]byte("data"), 10000)},
		}
		
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				kwikStreamID := uint64(100)
				offset := uint64(1024)
				
				// Encode
				encoded, err := protocol.EncapsulateData(kwikStreamID, offset, tc.data)
				require.NoError(t, err)
				
				// Decode
				metadata, decodedData, err := protocol.DecapsulateData(encoded)
				require.NoError(t, err)
				
				// Verify
				assert.Equal(t, kwikStreamID, metadata.KwikStreamID)
				assert.Equal(t, offset, metadata.Offset)
				assert.Equal(t, uint32(len(tc.data)), metadata.DataLength)
				assert.Equal(t, MetadataTypeData, metadata.MessageType)
				assert.Equal(t, tc.data, decodedData)
			})
		}
	})
	
	t.Run("round trip with different message types", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		messageTypes := []MetadataType{
			MetadataTypeData,
			MetadataTypeStreamOpen,
			MetadataTypeStreamClose,
			MetadataTypeOffsetSync,
		}
		
		for _, msgType := range messageTypes {
			t.Run(msgType.String(), func(t *testing.T) {
				metadata := createTestMetadata(100, 1024, 5, "path1", msgType)
				data := []byte("hello")
				
				// Encode
				encoded, err := protocol.encodeFrame(metadata, data)
				require.NoError(t, err)
				
				// Decode
				decodedMetadata, decodedData, err := protocol.DecapsulateData(encoded)
				require.NoError(t, err)
				
				// Verify
				assert.Equal(t, metadata.KwikStreamID, decodedMetadata.KwikStreamID)
				assert.Equal(t, metadata.Offset, decodedMetadata.Offset)
				assert.Equal(t, metadata.DataLength, decodedMetadata.DataLength)
				assert.Equal(t, metadata.MessageType, decodedMetadata.MessageType)
				assert.Equal(t, metadata.SourcePathID, decodedMetadata.SourcePathID)
				assert.Equal(t, data, decodedData)
			})
		}
	})
	
	t.Run("round trip with various path IDs", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		pathIDs := []string{
			"",
			"path1",
			"very-long-path-identifier-with-dashes-and-numbers-123",
			string(bytes.Repeat([]byte("x"), 255)), // Max length
		}
		
		for _, pathID := range pathIDs {
			t.Run(pathID, func(t *testing.T) {
				metadata := createTestMetadata(100, 1024, 5, pathID, MetadataTypeData)
				data := []byte("hello")
				
				// Encode
				encoded, err := protocol.encodeFrame(metadata, data)
				require.NoError(t, err)
				
				// Decode
				decodedMetadata, decodedData, err := protocol.DecapsulateData(encoded)
				require.NoError(t, err)
				
				// Verify
				assert.Equal(t, pathID, decodedMetadata.SourcePathID)
				assert.Equal(t, data, decodedData)
			})
		}
	})
}

// Tests for MetadataProtocolError
func TestMetadataProtocolError_Error(t *testing.T) {
	err := &MetadataProtocolError{
		Code:    "TEST_ERROR",
		Message: "test message",
	}
	
	assert.Equal(t, "TEST_ERROR: test message", err.Error())
}
//Tests for frame format validation
func TestFrameFormatValidation(t *testing.T) {
	t.Run("validates frame header structure", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create a valid frame
		data := []byte("test")
		encoded, err := protocol.EncapsulateData(100, 1024, data)
		require.NoError(t, err)
		
		// Verify frame structure
		assert.True(t, len(encoded) >= minMetadataFrameSize)
		
		// Check magic
		magic := binary.BigEndian.Uint32(encoded[0:4])
		assert.Equal(t, uint32(metadataFrameMagic), magic)
		
		// Check version
		version := encoded[4]
		assert.Equal(t, uint8(metadataFrameVersion), version)
		
		// Check message type
		msgType := encoded[5]
		assert.Equal(t, uint8(MetadataTypeData), msgType)
		
		// Check reserved bytes are zero
		reserved := binary.BigEndian.Uint16(encoded[6:8])
		assert.Equal(t, uint16(0), reserved)
	})
	
	t.Run("handles frame with maximum path ID length", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create path ID at maximum length
		maxPathID := string(bytes.Repeat([]byte("x"), protocol.maxPathIDLen))
		metadata := createTestMetadata(100, 1024, 5, maxPathID, MetadataTypeData)
		data := []byte("hello")
		
		// Encode
		encoded, err := protocol.encodeFrame(metadata, data)
		require.NoError(t, err)
		
		// Decode
		decodedMetadata, decodedData, err := protocol.DecapsulateData(encoded)
		require.NoError(t, err)
		
		// Verify
		assert.Equal(t, maxPathID, decodedMetadata.SourcePathID)
		assert.Equal(t, data, decodedData)
	})
	
	t.Run("handles frame with maximum data length", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create data at maximum length (but smaller for test performance)
		maxData := bytes.Repeat([]byte("x"), 50000) // Use smaller size for test
		
		// Encode
		encoded, err := protocol.EncapsulateData(100, 1024, maxData)
		require.NoError(t, err)
		
		// Decode
		metadata, decodedData, err := protocol.DecapsulateData(encoded)
		require.NoError(t, err)
		
		// Verify
		assert.Equal(t, uint32(len(maxData)), metadata.DataLength)
		assert.Equal(t, maxData, decodedData)
	})
}

// Tests for protocol error handling
func TestProtocolErrorHandling(t *testing.T) {
	t.Run("handles various corruption scenarios", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create a valid frame first
		originalData := []byte("test data")
		validFrame, err := protocol.EncapsulateData(100, 1024, originalData)
		require.NoError(t, err)
		
		corruptionTests := []struct {
			name        string
			corruptFunc func([]byte) []byte
			expectedErr string
		}{
			{
				name: "truncated frame",
				corruptFunc: func(frame []byte) []byte {
					return frame[:len(frame)/2]
				},
				expectedErr: ErrMetadataFrameTooSmall,
			},
			{
				name: "corrupted magic",
				corruptFunc: func(frame []byte) []byte {
					corrupted := make([]byte, len(frame))
					copy(corrupted, frame)
					binary.BigEndian.PutUint32(corrupted[0:4], 0xDEADBEEF)
					return corrupted
				},
				expectedErr: ErrMetadataInvalidMagic,
			},
			{
				name: "corrupted version",
				corruptFunc: func(frame []byte) []byte {
					corrupted := make([]byte, len(frame))
					copy(corrupted, frame)
					corrupted[4] = 99
					return corrupted
				},
				expectedErr: ErrMetadataUnsupportedVersion,
			},
		}
		
		for _, test := range corruptionTests {
			t.Run(test.name, func(t *testing.T) {
				corruptedFrame := test.corruptFunc(validFrame)
				
				_, _, err := protocol.DecapsulateData(corruptedFrame)
				
				assert.Error(t, err)
				protocolErr, ok := err.(*MetadataProtocolError)
				assert.True(t, ok, "Expected MetadataProtocolError")
				assert.Equal(t, test.expectedErr, protocolErr.Code)
			})
		}
	})
	
	t.Run("provides detailed error messages", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Test data too large error
		tooLargeData := make([]byte, protocol.maxDataLength+1)
		_, err := protocol.EncapsulateData(100, 0, tooLargeData)
		
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum")
		assert.Contains(t, err.Error(), "1048577") // maxDataLength + 1
		
		// Test invalid magic error
		invalidFrame := make([]byte, minMetadataFrameSize)
		binary.BigEndian.PutUint32(invalidFrame[0:4], 0x12345678)
		_, _, err = protocol.DecapsulateData(invalidFrame)
		
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid magic")
		assert.Contains(t, err.Error(), "0x4B574B4D") // Expected magic
		assert.Contains(t, err.Error(), "0x12345678") // Actual magic
	})
}

// Tests for performance and efficiency
func TestPerformanceAndEfficiency(t *testing.T) {
	t.Run("encapsulation is efficient for small data", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		smallData := []byte("hello")
		
		start := time.Now()
		for i := 0; i < 1000; i++ {
			_, err := protocol.EncapsulateData(100, uint64(i), smallData)
			require.NoError(t, err)
		}
		duration := time.Since(start)
		
		// Should complete 1000 encapsulations quickly
		assert.Less(t, duration.Nanoseconds(), (10*time.Millisecond).Nanoseconds())
	})
	
	t.Run("decapsulation is efficient for small data", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Pre-create encoded frames
		frames := make([][]byte, 1000)
		for i := 0; i < 1000; i++ {
			frame, err := protocol.EncapsulateData(100, uint64(i), []byte("hello"))
			require.NoError(t, err)
			frames[i] = frame
		}
		
		start := time.Now()
		for _, frame := range frames {
			_, _, err := protocol.DecapsulateData(frame)
			require.NoError(t, err)
		}
		duration := time.Since(start)
		
		// Should complete 1000 decapsulations quickly
		assert.Less(t, duration.Nanoseconds(), (10*time.Millisecond).Nanoseconds())
	})
	
	t.Run("overhead is reasonable", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		testCases := []struct {
			name     string
			dataSize int
		}{
			{"tiny", 1},
			{"small", 100},
			{"medium", 1000},
			{"large", 10000},
		}
		
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				data := bytes.Repeat([]byte("x"), tc.dataSize)
				
				encoded, err := protocol.EncapsulateData(100, 0, data)
				require.NoError(t, err)
				
				overhead := len(encoded) - len(data)
				overheadRatio := float64(overhead) / float64(len(data))
				
				// Overhead should be reasonable
				assert.Less(t, overhead, 100) // Less than 100 bytes overhead
				
				// For larger data, overhead ratio should be small
				if tc.dataSize >= 1000 {
					assert.Less(t, overheadRatio, 0.1) // Less than 10% overhead
				}
			})
		}
	})
}

// Tests for concurrent access (thread safety)
func TestMetadataProtocol_ConcurrentAccess(t *testing.T) {
	t.Run("concurrent encapsulation", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		const numGoroutines = 10
		const operationsPerGoroutine = 100
		
		done := make(chan bool, numGoroutines)
		
		for i := 0; i < numGoroutines; i++ {
			go func(goroutineID int) {
				defer func() { done <- true }()
				
				for j := 0; j < operationsPerGoroutine; j++ {
					kwikStreamID := uint64(goroutineID*operationsPerGoroutine + j + 1)
					offset := uint64(j * 10)
					data := []byte("test data")
					
					_, err := protocol.EncapsulateData(kwikStreamID, offset, data)
					assert.NoError(t, err)
				}
			}(i)
		}
		
		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			<-done
		}
	})
	
	t.Run("concurrent decapsulation", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Pre-create frames
		const numFrames = 100
		frames := make([][]byte, numFrames)
		
		for i := 0; i < numFrames; i++ {
			frame, err := protocol.EncapsulateData(uint64(i+1), uint64(i*10), []byte("test"))
			require.NoError(t, err)
			frames[i] = frame
		}
		
		const numGoroutines = 10
		done := make(chan bool, numGoroutines)
		
		for i := 0; i < numGoroutines; i++ {
			go func(goroutineID int) {
				defer func() { done <- true }()
				
				// Each goroutine processes a subset of frames
				start := goroutineID * (numFrames / numGoroutines)
				end := start + (numFrames / numGoroutines)
				
				for j := start; j < end; j++ {
					metadata, data, err := protocol.DecapsulateData(frames[j])
					assert.NoError(t, err)
					assert.NotNil(t, metadata)
					assert.NotNil(t, data)
				}
			}(i)
		}
		
		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			<-done
		}
	})
	
	t.Run("concurrent mixed operations", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		const numWorkers = 20
		const operationsPerWorker = 50
		
		done := make(chan bool, numWorkers)
		
		for i := 0; i < numWorkers; i++ {
			go func(workerID int) {
				defer func() { done <- true }()
				
				for j := 0; j < operationsPerWorker; j++ {
					if j%2 == 0 {
						// Encapsulate
						kwikStreamID := uint64(workerID*operationsPerWorker + j + 1)
						data := []byte("test data")
						_, err := protocol.EncapsulateData(kwikStreamID, uint64(j), data)
						assert.NoError(t, err)
					} else {
						// Create and validate metadata
						metadata := createTestMetadata(uint64(workerID+1), uint64(j), 10, "path1", MetadataTypeData)
						err := protocol.ValidateMetadata(metadata)
						assert.NoError(t, err)
					}
				}
			}(i)
		}
		
		// Wait for all workers to complete
		for i := 0; i < numWorkers; i++ {
			<-done
		}
	})
}

// Tests for edge cases and boundary conditions
func TestEdgeCasesAndBoundaryConditions(t *testing.T) {
	t.Run("handles zero values correctly", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Test with zero offset
		encoded, err := protocol.EncapsulateData(100, 0, []byte("test"))
		require.NoError(t, err)
		
		metadata, data, err := protocol.DecapsulateData(encoded)
		require.NoError(t, err)
		
		assert.Equal(t, uint64(0), metadata.Offset)
		assert.Equal(t, []byte("test"), data)
	})
	
	t.Run("handles maximum values correctly", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Test with maximum offset
		maxOffset := uint64(^uint64(0)) // Maximum uint64 value
		encoded, err := protocol.EncapsulateData(100, maxOffset, []byte("test"))
		require.NoError(t, err)
		
		metadata, data, err := protocol.DecapsulateData(encoded)
		require.NoError(t, err)
		
		assert.Equal(t, maxOffset, metadata.Offset)
		assert.Equal(t, []byte("test"), data)
	})
	
	t.Run("handles special characters in path ID", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		specialPathIDs := []string{
			"path-with-dashes",
			"path_with_underscores",
			"path.with.dots",
			"path/with/slashes",
			"path with spaces",
			"path123with456numbers",
			"UPPERCASE_PATH",
			"MiXeD_cAsE_pAtH",
		}
		
		for _, pathID := range specialPathIDs {
			t.Run(pathID, func(t *testing.T) {
				metadata := createTestMetadata(100, 1024, 5, pathID, MetadataTypeData)
				data := []byte("hello")
				
				// Encode
				encoded, err := protocol.encodeFrame(metadata, data)
				require.NoError(t, err)
				
				// Decode
				decodedMetadata, decodedData, err := protocol.DecapsulateData(encoded)
				require.NoError(t, err)
				
				// Verify
				assert.Equal(t, pathID, decodedMetadata.SourcePathID)
				assert.Equal(t, data, decodedData)
			})
		}
	})
	
	t.Run("handles binary data correctly", func(t *testing.T) {
		protocol := createTestMetadataProtocol()
		
		// Create binary data with all possible byte values
		binaryData := make([]byte, 256)
		for i := 0; i < 256; i++ {
			binaryData[i] = byte(i)
		}
		
		encoded, err := protocol.EncapsulateData(100, 1024, binaryData)
		require.NoError(t, err)
		
		metadata, decodedData, err := protocol.DecapsulateData(encoded)
		require.NoError(t, err)
		
		assert.Equal(t, uint32(256), metadata.DataLength)
		assert.Equal(t, binaryData, decodedData)
		
		// Verify each byte
		for i := 0; i < 256; i++ {
			assert.Equal(t, byte(i), decodedData[i], "Byte at position %d should be %d", i, i)
		}
	})
}