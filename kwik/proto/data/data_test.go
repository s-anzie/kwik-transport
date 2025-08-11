package data

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// Test DataFrame serialization/deserialization
func TestDataFrame_Serialization(t *testing.T) {
	original := &DataFrame{
		FrameId:         12345,
		LogicalStreamId: 67890,
		Offset:          1024,
		Data:            []byte("Hello, KWIK!"),
		Fin:             false,
		Timestamp:       uint64(time.Now().UnixNano()),
		PathId:          "data-path-1",
		DataLength:      12,
		Checksum:        0x12345678,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &DataFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.FrameId, deserialized.FrameId)
	assert.Equal(t, original.LogicalStreamId, deserialized.LogicalStreamId)
	assert.Equal(t, original.Offset, deserialized.Offset)
	assert.Equal(t, original.Data, deserialized.Data)
	assert.Equal(t, original.Fin, deserialized.Fin)
	assert.Equal(t, original.Timestamp, deserialized.Timestamp)
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.DataLength, deserialized.DataLength)
	assert.Equal(t, original.Checksum, deserialized.Checksum)
}

// Test DataFrame with FIN flag
func TestDataFrame_WithFin_Serialization(t *testing.T) {
	original := &DataFrame{
		FrameId:         99999,
		LogicalStreamId: 11111,
		Offset:          2048,
		Data:            []byte("Final frame"),
		Fin:             true,
		Timestamp:       uint64(time.Now().UnixNano()),
		PathId:          "final-path",
		DataLength:      11,
		Checksum:        0xABCDEF00,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &DataFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify FIN flag is preserved
	assert.True(t, deserialized.Fin)
	assert.Equal(t, original.Data, deserialized.Data)
}

// Test DataPacket serialization/deserialization
func TestDataPacket_Serialization(t *testing.T) {
	frame1 := &DataFrame{
		FrameId:         1,
		LogicalStreamId: 100,
		Offset:          0,
		Data:            []byte("Frame 1"),
		Fin:             false,
		Timestamp:       uint64(time.Now().UnixNano()),
		PathId:          "packet-path",
		DataLength:      7,
		Checksum:        0x11111111,
	}

	frame2 := &DataFrame{
		FrameId:         2,
		LogicalStreamId: 100,
		Offset:          7,
		Data:            []byte("Frame 2"),
		Fin:             true,
		Timestamp:       uint64(time.Now().UnixNano()),
		PathId:          "packet-path",
		DataLength:      7,
		Checksum:        0x22222222,
	}

	original := &DataPacket{
		PacketId:  54321,
		PathId:    "packet-path",
		Frames:    []*DataFrame{frame1, frame2},
		Checksum:  0x87654321,
		Timestamp: uint64(time.Now().UnixNano()),
		Metadata: &PacketMetadata{
			TotalSize:      100,
			FrameCount:     2,
			Compression:    CompressionType_COMPRESSION_NONE,
			SequenceNumber: 1,
			Retransmission: false,
		},
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &DataPacket{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify packet fields
	assert.Equal(t, original.PacketId, deserialized.PacketId)
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.Checksum, deserialized.Checksum)
	assert.Equal(t, original.Timestamp, deserialized.Timestamp)

	// Verify frames
	assert.Len(t, deserialized.Frames, 2)
	assert.Equal(t, frame1.FrameId, deserialized.Frames[0].FrameId)
	assert.Equal(t, frame1.Data, deserialized.Frames[0].Data)
	assert.Equal(t, frame2.FrameId, deserialized.Frames[1].FrameId)
	assert.Equal(t, frame2.Data, deserialized.Frames[1].Data)
	assert.True(t, deserialized.Frames[1].Fin)

	// Verify metadata
	assert.NotNil(t, deserialized.Metadata)
	assert.Equal(t, original.Metadata.TotalSize, deserialized.Metadata.TotalSize)
	assert.Equal(t, original.Metadata.FrameCount, deserialized.Metadata.FrameCount)
	assert.Equal(t, original.Metadata.Compression, deserialized.Metadata.Compression)
	assert.Equal(t, original.Metadata.SequenceNumber, deserialized.Metadata.SequenceNumber)
	assert.Equal(t, original.Metadata.Retransmission, deserialized.Metadata.Retransmission)
}

// Test PacketMetadata serialization/deserialization
func TestPacketMetadata_Serialization(t *testing.T) {
	original := &PacketMetadata{
		TotalSize:      2048,
		FrameCount:     5,
		Compression:    CompressionType_COMPRESSION_GZIP,
		SequenceNumber: 42,
		Retransmission: true,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &PacketMetadata{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.TotalSize, deserialized.TotalSize)
	assert.Equal(t, original.FrameCount, deserialized.FrameCount)
	assert.Equal(t, original.Compression, deserialized.Compression)
	assert.Equal(t, original.SequenceNumber, deserialized.SequenceNumber)
	assert.Equal(t, original.Retransmission, deserialized.Retransmission)
}

// Test AckFrame serialization/deserialization
func TestAckFrame_Serialization(t *testing.T) {
	original := &AckFrame{
		AckId:           98765,
		AckedPacketIds:  []uint64{1, 2, 3, 5, 8, 13},
		PathId:          "ack-path",
		Timestamp:       uint64(time.Now().UnixNano()),
		AckRanges: []*AckRange{
			{Start: 1, End: 3},
			{Start: 5, End: 5},
			{Start: 8, End: 13},
		},
		LargestAcked: 13,
		AckDelay:     1500, // 1.5ms in microseconds
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &AckFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.AckId, deserialized.AckId)
	assert.Equal(t, original.AckedPacketIds, deserialized.AckedPacketIds)
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.Timestamp, deserialized.Timestamp)
	assert.Equal(t, original.LargestAcked, deserialized.LargestAcked)
	assert.Equal(t, original.AckDelay, deserialized.AckDelay)

	// Verify ACK ranges
	assert.Len(t, deserialized.AckRanges, 3)
	for i, expectedRange := range original.AckRanges {
		assert.Equal(t, expectedRange.Start, deserialized.AckRanges[i].Start)
		assert.Equal(t, expectedRange.End, deserialized.AckRanges[i].End)
	}
}

// Test AckRange serialization/deserialization
func TestAckRange_Serialization(t *testing.T) {
	original := &AckRange{
		Start: 100,
		End:   200,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &AckRange{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.Start, deserialized.Start)
	assert.Equal(t, original.End, deserialized.End)
}

// Test StreamFlowControlFrame serialization/deserialization
func TestStreamFlowControlFrame_Serialization(t *testing.T) {
	original := &StreamFlowControlFrame{
		LogicalStreamId:   777,
		MaxStreamData:     65536,
		StreamDataBlocked: 32768,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &StreamFlowControlFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.LogicalStreamId, deserialized.LogicalStreamId)
	assert.Equal(t, original.MaxStreamData, deserialized.MaxStreamData)
	assert.Equal(t, original.StreamDataBlocked, deserialized.StreamDataBlocked)
}

// Test ConnectionFlowControlFrame serialization/deserialization
func TestConnectionFlowControlFrame_Serialization(t *testing.T) {
	original := &ConnectionFlowControlFrame{
		PathId:      "flow-control-path",
		MaxData:     1048576, // 1MB
		DataBlocked: 524288,  // 512KB
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &ConnectionFlowControlFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.MaxData, deserialized.MaxData)
	assert.Equal(t, original.DataBlocked, deserialized.DataBlocked)
}

// Test StreamResetFrame serialization/deserialization
func TestStreamResetFrame_Serialization(t *testing.T) {
	original := &StreamResetFrame{
		LogicalStreamId:      888,
		ApplicationErrorCode: 0x1001,
		FinalSize:            4096,
		Reason:               "Application requested reset",
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &StreamResetFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.LogicalStreamId, deserialized.LogicalStreamId)
	assert.Equal(t, original.ApplicationErrorCode, deserialized.ApplicationErrorCode)
	assert.Equal(t, original.FinalSize, deserialized.FinalSize)
	assert.Equal(t, original.Reason, deserialized.Reason)
}

// Test StopSendingFrame serialization/deserialization
func TestStopSendingFrame_Serialization(t *testing.T) {
	original := &StopSendingFrame{
		LogicalStreamId:      999,
		ApplicationErrorCode: 0x2002,
		Reason:               "Client no longer interested",
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &StopSendingFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.LogicalStreamId, deserialized.LogicalStreamId)
	assert.Equal(t, original.ApplicationErrorCode, deserialized.ApplicationErrorCode)
	assert.Equal(t, original.Reason, deserialized.Reason)
}

// Test CryptoFrame serialization/deserialization
func TestCryptoFrame_Serialization(t *testing.T) {
	original := &CryptoFrame{
		Offset:     256,
		CryptoData: []byte("TLS handshake data"),
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &CryptoFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.Offset, deserialized.Offset)
	assert.Equal(t, original.CryptoData, deserialized.CryptoData)
}

// Test PathChallengeFrame serialization/deserialization
func TestPathChallengeFrame_Serialization(t *testing.T) {
	original := &PathChallengeFrame{
		ChallengeData: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		PathId:        "challenge-path",
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &PathChallengeFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.ChallengeData, deserialized.ChallengeData)
	assert.Equal(t, original.PathId, deserialized.PathId)
}

// Test PathResponseFrame serialization/deserialization
func TestPathResponseFrame_Serialization(t *testing.T) {
	original := &PathResponseFrame{
		ResponseData: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		PathId:       "response-path",
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &PathResponseFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.ResponseData, deserialized.ResponseData)
	assert.Equal(t, original.PathId, deserialized.PathId)
}

// Test ConnectionCloseFrame serialization/deserialization
func TestConnectionCloseFrame_Serialization(t *testing.T) {
	original := &ConnectionCloseFrame{
		ErrorCode:        0x3003,
		FrameType:        0x04,
		ReasonPhrase:     "Connection terminated by peer",
		ApplicationError: true,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &ConnectionCloseFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.ErrorCode, deserialized.ErrorCode)
	assert.Equal(t, original.FrameType, deserialized.FrameType)
	assert.Equal(t, original.ReasonPhrase, deserialized.ReasonPhrase)
	assert.Equal(t, original.ApplicationError, deserialized.ApplicationError)
}

// Test PingFrame serialization/deserialization
func TestPingFrame_Serialization(t *testing.T) {
	original := &PingFrame{
		Timestamp: uint64(time.Now().UnixNano()),
		PingData:  []byte("ping test data"),
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &PingFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.Timestamp, deserialized.Timestamp)
	assert.Equal(t, original.PingData, deserialized.PingData)
}

// Test StreamState serialization/deserialization
func TestStreamState_Serialization(t *testing.T) {
	original := &StreamState{
		LogicalStreamId:        123,
		State:                  StreamStateType_DATA_STREAM_OPEN,
		BytesSent:              1024,
		BytesReceived:          2048,
		MaxStreamDataSent:      4096,
		MaxStreamDataReceived:  8192,
		FinSent:                false,
		FinReceived:            true,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &StreamState{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.LogicalStreamId, deserialized.LogicalStreamId)
	assert.Equal(t, original.State, deserialized.State)
	assert.Equal(t, original.BytesSent, deserialized.BytesSent)
	assert.Equal(t, original.BytesReceived, deserialized.BytesReceived)
	assert.Equal(t, original.MaxStreamDataSent, deserialized.MaxStreamDataSent)
	assert.Equal(t, original.MaxStreamDataReceived, deserialized.MaxStreamDataReceived)
	assert.Equal(t, original.FinSent, deserialized.FinSent)
	assert.Equal(t, original.FinReceived, deserialized.FinReceived)
}

// Test AggregatedDataStats serialization/deserialization
func TestAggregatedDataStats_Serialization(t *testing.T) {
	pathStats1 := &PathDataStats{
		PathId:          "path-1",
		BytesSent:       1024,
		BytesReceived:   2048,
		PacketsSent:     10,
		PacketsReceived: 15,
		Retransmissions: 2,
		LossRate:        0.01,
		RttMs:           25,
	}

	pathStats2 := &PathDataStats{
		PathId:          "path-2",
		BytesSent:       2048,
		BytesReceived:   4096,
		PacketsSent:     20,
		PacketsReceived: 30,
		Retransmissions: 1,
		LossRate:        0.005,
		RttMs:           30,
	}

	original := &AggregatedDataStats{
		TotalBytesSent:       3072,
		TotalBytesReceived:   6144,
		TotalPacketsSent:     30,
		TotalPacketsReceived: 45,
		PathStats:            []*PathDataStats{pathStats1, pathStats2},
		AggregationTimestamp: uint64(time.Now().UnixNano()),
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &AggregatedDataStats{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify aggregate fields
	assert.Equal(t, original.TotalBytesSent, deserialized.TotalBytesSent)
	assert.Equal(t, original.TotalBytesReceived, deserialized.TotalBytesReceived)
	assert.Equal(t, original.TotalPacketsSent, deserialized.TotalPacketsSent)
	assert.Equal(t, original.TotalPacketsReceived, deserialized.TotalPacketsReceived)
	assert.Equal(t, original.AggregationTimestamp, deserialized.AggregationTimestamp)

	// Verify path stats
	assert.Len(t, deserialized.PathStats, 2)
	for i, expectedStats := range original.PathStats {
		actualStats := deserialized.PathStats[i]
		assert.Equal(t, expectedStats.PathId, actualStats.PathId)
		assert.Equal(t, expectedStats.BytesSent, actualStats.BytesSent)
		assert.Equal(t, expectedStats.BytesReceived, actualStats.BytesReceived)
		assert.Equal(t, expectedStats.PacketsSent, actualStats.PacketsSent)
		assert.Equal(t, expectedStats.PacketsReceived, actualStats.PacketsReceived)
		assert.Equal(t, expectedStats.Retransmissions, actualStats.Retransmissions)
		assert.Equal(t, expectedStats.LossRate, actualStats.LossRate)
		assert.Equal(t, expectedStats.RttMs, actualStats.RttMs)
	}
}

// Test CompressionType enum values
func TestCompressionType_EnumValues(t *testing.T) {
	tests := []struct {
		name     string
		enumVal  CompressionType
		expected int32
	}{
		{"COMPRESSION_NONE", CompressionType_COMPRESSION_NONE, 0},
		{"COMPRESSION_GZIP", CompressionType_COMPRESSION_GZIP, 1},
		{"COMPRESSION_LZ4", CompressionType_COMPRESSION_LZ4, 2},
		{"COMPRESSION_ZSTD", CompressionType_COMPRESSION_ZSTD, 3},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, int32(test.enumVal))
		})
	}
}

// Test StreamStateType enum values
func TestStreamStateType_EnumValues(t *testing.T) {
	tests := []struct {
		name     string
		enumVal  StreamStateType
		expected int32
	}{
		{"DATA_STREAM_IDLE", StreamStateType_DATA_STREAM_IDLE, 0},
		{"DATA_STREAM_OPEN", StreamStateType_DATA_STREAM_OPEN, 1},
		{"DATA_STREAM_HALF_CLOSED_LOCAL", StreamStateType_DATA_STREAM_HALF_CLOSED_LOCAL, 2},
		{"DATA_STREAM_HALF_CLOSED_REMOTE", StreamStateType_DATA_STREAM_HALF_CLOSED_REMOTE, 3},
		{"DATA_STREAM_CLOSED", StreamStateType_DATA_STREAM_CLOSED, 4},
		{"DATA_STREAM_RESET_SENT", StreamStateType_DATA_STREAM_RESET_SENT, 5},
		{"DATA_STREAM_RESET_RECEIVED", StreamStateType_DATA_STREAM_RESET_RECEIVED, 6},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, int32(test.enumVal))
		})
	}
}

// Test DataFrameType enum values
func TestDataFrameType_EnumValues(t *testing.T) {
	tests := []struct {
		name     string
		enumVal  DataFrameType
		expected int32
	}{
		{"DATA_FRAME_DATA", DataFrameType_DATA_FRAME_DATA, 0},
		{"DATA_FRAME_ACK", DataFrameType_DATA_FRAME_ACK, 1},
		{"DATA_FRAME_STREAM_FLOW_CONTROL", DataFrameType_DATA_FRAME_STREAM_FLOW_CONTROL, 2},
		{"DATA_FRAME_CONNECTION_FLOW_CONTROL", DataFrameType_DATA_FRAME_CONNECTION_FLOW_CONTROL, 3},
		{"DATA_FRAME_STREAM_RESET", DataFrameType_DATA_FRAME_STREAM_RESET, 4},
		{"DATA_FRAME_STOP_SENDING", DataFrameType_DATA_FRAME_STOP_SENDING, 5},
		{"DATA_FRAME_CRYPTO", DataFrameType_DATA_FRAME_CRYPTO, 6},
		{"DATA_FRAME_NEW_CONNECTION_ID", DataFrameType_DATA_FRAME_NEW_CONNECTION_ID, 7},
		{"DATA_FRAME_RETIRE_CONNECTION_ID", DataFrameType_DATA_FRAME_RETIRE_CONNECTION_ID, 8},
		{"DATA_FRAME_PATH_CHALLENGE", DataFrameType_DATA_FRAME_PATH_CHALLENGE, 9},
		{"DATA_FRAME_PATH_RESPONSE", DataFrameType_DATA_FRAME_PATH_RESPONSE, 10},
		{"DATA_FRAME_CONNECTION_CLOSE", DataFrameType_DATA_FRAME_CONNECTION_CLOSE, 11},
		{"DATA_FRAME_PADDING", DataFrameType_DATA_FRAME_PADDING, 12},
		{"DATA_FRAME_PING", DataFrameType_DATA_FRAME_PING, 13},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, int32(test.enumVal))
		})
	}
}

// Test large data frame serialization
func TestLargeDataFrame_Serialization(t *testing.T) {
	// Create a large data payload (100KB)
	largeData := make([]byte, 100*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	original := &DataFrame{
		FrameId:         999999,
		LogicalStreamId: 888888,
		Offset:          0,
		Data:            largeData,
		Fin:             false,
		Timestamp:       uint64(time.Now().UnixNano()),
		PathId:          "large-data-path",
		DataLength:      uint32(len(largeData)),
		Checksum:        0xDEADBEEF,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &DataFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match, including the large data
	assert.Equal(t, original.FrameId, deserialized.FrameId)
	assert.Equal(t, original.LogicalStreamId, deserialized.LogicalStreamId)
	assert.Equal(t, original.Offset, deserialized.Offset)
	assert.Equal(t, original.Data, deserialized.Data)
	assert.Equal(t, original.Fin, deserialized.Fin)
	assert.Equal(t, original.Timestamp, deserialized.Timestamp)
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.DataLength, deserialized.DataLength)
	assert.Equal(t, original.Checksum, deserialized.Checksum)
}

// Test empty data frame serialization
func TestEmptyDataFrame_Serialization(t *testing.T) {
	original := &DataFrame{}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotNil(t, data)

	// Deserialize
	deserialized := &DataFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify default values
	assert.Equal(t, uint64(0), deserialized.FrameId)
	assert.Equal(t, uint64(0), deserialized.LogicalStreamId)
	assert.Equal(t, uint64(0), deserialized.Offset)
	assert.Nil(t, deserialized.Data)
	assert.False(t, deserialized.Fin)
	assert.Equal(t, uint64(0), deserialized.Timestamp)
	assert.Equal(t, "", deserialized.PathId)
	assert.Equal(t, uint32(0), deserialized.DataLength)
	assert.Equal(t, uint32(0), deserialized.Checksum)
}

// Test malformed data deserialization
func TestMalformedDataFrame_Deserialization(t *testing.T) {
	malformedData := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}

	frame := &DataFrame{}
	err := proto.Unmarshal(malformedData, frame)

	// Should return an error for malformed data
	assert.Error(t, err)
}