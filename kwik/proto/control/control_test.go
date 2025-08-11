package control

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// Test ControlFrame serialization/deserialization
func TestControlFrame_Serialization(t *testing.T) {
	original := &ControlFrame{
		FrameId:      12345,
		Type:         ControlFrameType_ADD_PATH_REQUEST,
		Payload:      []byte("test payload"),
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: "source-path-1",
		TargetPathId: "target-path-1",
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &ControlFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.FrameId, deserialized.FrameId)
	assert.Equal(t, original.Type, deserialized.Type)
	assert.Equal(t, original.Payload, deserialized.Payload)
	assert.Equal(t, original.Timestamp, deserialized.Timestamp)
	assert.Equal(t, original.SourcePathId, deserialized.SourcePathId)
	assert.Equal(t, original.TargetPathId, deserialized.TargetPathId)
}

// Test AddPathRequest serialization/deserialization
func TestAddPathRequest_Serialization(t *testing.T) {
	original := &AddPathRequest{
		TargetAddress: "127.0.0.1:8080",
		SessionId:     "session-123",
		Priority:      1,
		Metadata: map[string]string{
			"region": "us-west",
			"zone":   "us-west-1a",
		},
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &AddPathRequest{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.TargetAddress, deserialized.TargetAddress)
	assert.Equal(t, original.SessionId, deserialized.SessionId)
	assert.Equal(t, original.Priority, deserialized.Priority)
	assert.Equal(t, original.Metadata, deserialized.Metadata)
}

// Test AddPathResponse serialization/deserialization
func TestAddPathResponse_Serialization(t *testing.T) {
	original := &AddPathResponse{
		Success:          true,
		PathId:           "path-456",
		ErrorMessage:     "",
		ErrorCode:        "",
		ConnectionTimeMs: 150,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &AddPathResponse{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.Success, deserialized.Success)
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.ErrorMessage, deserialized.ErrorMessage)
	assert.Equal(t, original.ErrorCode, deserialized.ErrorCode)
	assert.Equal(t, original.ConnectionTimeMs, deserialized.ConnectionTimeMs)
}

// Test AddPathResponse with error
func TestAddPathResponse_WithError_Serialization(t *testing.T) {
	original := &AddPathResponse{
		Success:          false,
		PathId:           "",
		ErrorMessage:     "Connection failed",
		ErrorCode:        "CONNECTION_TIMEOUT",
		ConnectionTimeMs: 0,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &AddPathResponse{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.Success, deserialized.Success)
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.ErrorMessage, deserialized.ErrorMessage)
	assert.Equal(t, original.ErrorCode, deserialized.ErrorCode)
	assert.Equal(t, original.ConnectionTimeMs, deserialized.ConnectionTimeMs)
}

// Test RemovePathRequest serialization/deserialization
func TestRemovePathRequest_Serialization(t *testing.T) {
	original := &RemovePathRequest{
		PathId:    "path-789",
		Reason:    "Server maintenance",
		Graceful:  true,
		TimeoutMs: 5000,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &RemovePathRequest{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.Reason, deserialized.Reason)
	assert.Equal(t, original.Graceful, deserialized.Graceful)
	assert.Equal(t, original.TimeoutMs, deserialized.TimeoutMs)
}

// Test RemovePathResponse serialization/deserialization
func TestRemovePathResponse_Serialization(t *testing.T) {
	original := &RemovePathResponse{
		Success:         true,
		PathId:          "path-789",
		ErrorMessage:    "",
		StreamsMigrated: 5,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &RemovePathResponse{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.Success, deserialized.Success)
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.ErrorMessage, deserialized.ErrorMessage)
	assert.Equal(t, original.StreamsMigrated, deserialized.StreamsMigrated)
}

// Test PathStatusNotification serialization/deserialization
func TestPathStatusNotification_Serialization(t *testing.T) {
	original := &PathStatusNotification{
		PathId:    "path-101",
		Status:    PathStatus_CONTROL_PATH_DEAD,
		Reason:    "Connection timeout",
		Timestamp: uint64(time.Now().UnixNano()),
		Metrics: &PathMetrics{
			RttMs:           50,
			BandwidthBps:    1000000,
			PacketLossRate:  0.01,
			BytesSent:       1024,
			BytesReceived:   2048,
			LastActivity:    uint64(time.Now().UnixNano()),
		},
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &PathStatusNotification{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.Status, deserialized.Status)
	assert.Equal(t, original.Reason, deserialized.Reason)
	assert.Equal(t, original.Timestamp, deserialized.Timestamp)
	
	// Verify metrics
	assert.NotNil(t, deserialized.Metrics)
	assert.Equal(t, original.Metrics.RttMs, deserialized.Metrics.RttMs)
	assert.Equal(t, original.Metrics.BandwidthBps, deserialized.Metrics.BandwidthBps)
	assert.Equal(t, original.Metrics.PacketLossRate, deserialized.Metrics.PacketLossRate)
	assert.Equal(t, original.Metrics.BytesSent, deserialized.Metrics.BytesSent)
	assert.Equal(t, original.Metrics.BytesReceived, deserialized.Metrics.BytesReceived)
	assert.Equal(t, original.Metrics.LastActivity, deserialized.Metrics.LastActivity)
}

// Test PathMetrics serialization/deserialization
func TestPathMetrics_Serialization(t *testing.T) {
	original := &PathMetrics{
		RttMs:           25,
		BandwidthBps:    5000000,
		PacketLossRate:  0.005,
		BytesSent:       10240,
		BytesReceived:   20480,
		LastActivity:    uint64(time.Now().UnixNano()),
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &PathMetrics{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.RttMs, deserialized.RttMs)
	assert.Equal(t, original.BandwidthBps, deserialized.BandwidthBps)
	assert.Equal(t, original.PacketLossRate, deserialized.PacketLossRate)
	assert.Equal(t, original.BytesSent, deserialized.BytesSent)
	assert.Equal(t, original.BytesReceived, deserialized.BytesReceived)
	assert.Equal(t, original.LastActivity, deserialized.LastActivity)
}

// Test AuthenticationRequest serialization/deserialization
func TestAuthenticationRequest_Serialization(t *testing.T) {
	original := &AuthenticationRequest{
		SessionId:         "session-abc123",
		Credentials:       []byte("secret-credentials"),
		ClientVersion:     "kwik-1.0.0",
		SupportedFeatures: []string{"multipath", "aggregation", "migration"},
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &AuthenticationRequest{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.SessionId, deserialized.SessionId)
	assert.Equal(t, original.Credentials, deserialized.Credentials)
	assert.Equal(t, original.ClientVersion, deserialized.ClientVersion)
	assert.Equal(t, original.SupportedFeatures, deserialized.SupportedFeatures)
}

// Test AuthenticationResponse serialization/deserialization
func TestAuthenticationResponse_Serialization(t *testing.T) {
	original := &AuthenticationResponse{
		Success:         true,
		SessionId:       "session-abc123",
		ErrorMessage:    "",
		ServerVersion:   "kwik-server-1.0.0",
		EnabledFeatures: []string{"multipath", "aggregation"},
		SessionTimeout:  3600,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &AuthenticationResponse{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.Success, deserialized.Success)
	assert.Equal(t, original.SessionId, deserialized.SessionId)
	assert.Equal(t, original.ErrorMessage, deserialized.ErrorMessage)
	assert.Equal(t, original.ServerVersion, deserialized.ServerVersion)
	assert.Equal(t, original.EnabledFeatures, deserialized.EnabledFeatures)
	assert.Equal(t, original.SessionTimeout, deserialized.SessionTimeout)
}

// Test StreamCreateNotification serialization/deserialization
func TestStreamCreateNotification_Serialization(t *testing.T) {
	original := &StreamCreateNotification{
		LogicalStreamId: 42,
		PathId:          "path-stream-1",
		StreamType:      StreamType_CONTROL_STREAM_BIDIRECTIONAL,
		Priority:        2,
		Metadata: map[string]string{
			"application": "http",
			"version":     "2.0",
		},
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &StreamCreateNotification{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.LogicalStreamId, deserialized.LogicalStreamId)
	assert.Equal(t, original.PathId, deserialized.PathId)
	assert.Equal(t, original.StreamType, deserialized.StreamType)
	assert.Equal(t, original.Priority, deserialized.Priority)
	assert.Equal(t, original.Metadata, deserialized.Metadata)
}

// Test RawPacketTransmission serialization/deserialization
func TestRawPacketTransmission_Serialization(t *testing.T) {
	original := &RawPacketTransmission{
		Data:           []byte("raw packet data"),
		TargetPathId:   "target-path-raw",
		SourceServerId: "server-1",
		ProtocolHint:   "custom-protocol",
		PreserveOrder:  true,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &RawPacketTransmission{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.Data, deserialized.Data)
	assert.Equal(t, original.TargetPathId, deserialized.TargetPathId)
	assert.Equal(t, original.SourceServerId, deserialized.SourceServerId)
	assert.Equal(t, original.ProtocolHint, deserialized.ProtocolHint)
	assert.Equal(t, original.PreserveOrder, deserialized.PreserveOrder)
}

// Test Heartbeat serialization/deserialization
func TestHeartbeat_Serialization(t *testing.T) {
	original := &Heartbeat{
		SequenceNumber: 100,
		Timestamp:      uint64(time.Now().UnixNano()),
		EchoData:       []byte("ping data"),
		CurrentMetrics: &PathMetrics{
			RttMs:           30,
			BandwidthBps:    2000000,
			PacketLossRate:  0.002,
			BytesSent:       5120,
			BytesReceived:   10240,
			LastActivity:    uint64(time.Now().UnixNano()),
		},
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &Heartbeat{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.SequenceNumber, deserialized.SequenceNumber)
	assert.Equal(t, original.Timestamp, deserialized.Timestamp)
	assert.Equal(t, original.EchoData, deserialized.EchoData)
	
	// Verify metrics
	assert.NotNil(t, deserialized.CurrentMetrics)
	assert.Equal(t, original.CurrentMetrics.RttMs, deserialized.CurrentMetrics.RttMs)
	assert.Equal(t, original.CurrentMetrics.BandwidthBps, deserialized.CurrentMetrics.BandwidthBps)
}

// Test SessionClose serialization/deserialization
func TestSessionClose_Serialization(t *testing.T) {
	original := &SessionClose{
		Reason:         "Client requested shutdown",
		TimeoutMs:      10000,
		MigrateStreams: true,
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &SessionClose{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match
	assert.Equal(t, original.Reason, deserialized.Reason)
	assert.Equal(t, original.TimeoutMs, deserialized.TimeoutMs)
	assert.Equal(t, original.MigrateStreams, deserialized.MigrateStreams)
}

// Test ControlFrameType enum values
func TestControlFrameType_EnumValues(t *testing.T) {
	tests := []struct {
		name     string
		enumVal  ControlFrameType
		expected int32
	}{
		{"ADD_PATH_REQUEST", ControlFrameType_ADD_PATH_REQUEST, 0},
		{"ADD_PATH_RESPONSE", ControlFrameType_ADD_PATH_RESPONSE, 1},
		{"REMOVE_PATH_REQUEST", ControlFrameType_REMOVE_PATH_REQUEST, 2},
		{"REMOVE_PATH_RESPONSE", ControlFrameType_REMOVE_PATH_RESPONSE, 3},
		{"PATH_STATUS_NOTIFICATION", ControlFrameType_PATH_STATUS_NOTIFICATION, 4},
		{"AUTHENTICATION_REQUEST", ControlFrameType_AUTHENTICATION_REQUEST, 5},
		{"AUTHENTICATION_RESPONSE", ControlFrameType_AUTHENTICATION_RESPONSE, 6},
		{"STREAM_CREATE_NOTIFICATION", ControlFrameType_STREAM_CREATE_NOTIFICATION, 7},
		{"RAW_PACKET_TRANSMISSION", ControlFrameType_RAW_PACKET_TRANSMISSION, 8},
		{"HEARTBEAT", ControlFrameType_HEARTBEAT, 9},
		{"SESSION_CLOSE", ControlFrameType_SESSION_CLOSE, 10},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, int32(test.enumVal))
		})
	}
}

// Test PathStatus enum values
func TestPathStatus_EnumValues(t *testing.T) {
	tests := []struct {
		name     string
		enumVal  PathStatus
		expected int32
	}{
		{"CONTROL_PATH_ACTIVE", PathStatus_CONTROL_PATH_ACTIVE, 0},
		{"CONTROL_PATH_DEAD", PathStatus_CONTROL_PATH_DEAD, 1},
		{"CONTROL_PATH_CONNECTING", PathStatus_CONTROL_PATH_CONNECTING, 2},
		{"CONTROL_PATH_DISCONNECTING", PathStatus_CONTROL_PATH_DISCONNECTING, 3},
		{"CONTROL_PATH_DEGRADED", PathStatus_CONTROL_PATH_DEGRADED, 4},
		{"CONTROL_PATH_RECOVERING", PathStatus_CONTROL_PATH_RECOVERING, 5},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, int32(test.enumVal))
		})
	}
}

// Test StreamType enum values
func TestStreamType_EnumValues(t *testing.T) {
	tests := []struct {
		name     string
		enumVal  StreamType
		expected int32
	}{
		{"CONTROL_STREAM_BIDIRECTIONAL", StreamType_CONTROL_STREAM_BIDIRECTIONAL, 0},
		{"CONTROL_STREAM_UNIDIRECTIONAL", StreamType_CONTROL_STREAM_UNIDIRECTIONAL, 1},
		{"CONTROL_STREAM_CONTROL", StreamType_CONTROL_STREAM_CONTROL, 2},
		{"CONTROL_STREAM_DATA", StreamType_CONTROL_STREAM_DATA, 3},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, int32(test.enumVal))
		})
	}
}

// Test empty message serialization
func TestEmptyMessage_Serialization(t *testing.T) {
	original := &ControlFrame{}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotNil(t, data) // Should not be nil, even if empty

	// Deserialize
	deserialized := &ControlFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify default values
	assert.Equal(t, uint64(0), deserialized.FrameId)
	assert.Equal(t, ControlFrameType_ADD_PATH_REQUEST, deserialized.Type) // Default enum value
	assert.Nil(t, deserialized.Payload)
	assert.Equal(t, uint64(0), deserialized.Timestamp)
	assert.Equal(t, "", deserialized.SourcePathId)
	assert.Equal(t, "", deserialized.TargetPathId)
}

// Test large payload serialization
func TestLargePayload_Serialization(t *testing.T) {
	// Create a large payload (1MB)
	largePayload := make([]byte, 1024*1024)
	for i := range largePayload {
		largePayload[i] = byte(i % 256)
	}

	original := &ControlFrame{
		FrameId:      999999,
		Type:         ControlFrameType_RAW_PACKET_TRANSMISSION,
		Payload:      largePayload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: "large-payload-source",
		TargetPathId: "large-payload-target",
	}

	// Serialize
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	deserialized := &ControlFrame{}
	err = proto.Unmarshal(data, deserialized)
	require.NoError(t, err)

	// Verify all fields match, including the large payload
	assert.Equal(t, original.FrameId, deserialized.FrameId)
	assert.Equal(t, original.Type, deserialized.Type)
	assert.Equal(t, original.Payload, deserialized.Payload)
	assert.Equal(t, original.Timestamp, deserialized.Timestamp)
	assert.Equal(t, original.SourcePathId, deserialized.SourcePathId)
	assert.Equal(t, original.TargetPathId, deserialized.TargetPathId)
}

// Test malformed data deserialization
func TestMalformedData_Deserialization(t *testing.T) {
	malformedData := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}

	frame := &ControlFrame{}
	err := proto.Unmarshal(malformedData, frame)

	// Should return an error for malformed data
	assert.Error(t, err)
}

// Test nested message serialization with all fields
func TestComplexNestedMessage_Serialization(t *testing.T) {
	// Create a complex control frame with nested AddPathRequest
	addPathReq := &AddPathRequest{
		TargetAddress: "192.168.1.100:9090",
		SessionId:     "complex-session-456",
		Priority:      3,
		Metadata: map[string]string{
			"datacenter": "east-1",
			"rack":       "A-42",
			"server":     "web-01",
		},
	}

	// Serialize the nested message
	payload, err := proto.Marshal(addPathReq)
	require.NoError(t, err)

	// Create the control frame
	original := &ControlFrame{
		FrameId:      777888,
		Type:         ControlFrameType_ADD_PATH_REQUEST,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: "complex-source",
		TargetPathId: "complex-target",
	}

	// Serialize the control frame
	data, err := proto.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize the control frame
	deserializedFrame := &ControlFrame{}
	err = proto.Unmarshal(data, deserializedFrame)
	require.NoError(t, err)

	// Verify frame fields
	assert.Equal(t, original.FrameId, deserializedFrame.FrameId)
	assert.Equal(t, original.Type, deserializedFrame.Type)
	assert.Equal(t, original.Timestamp, deserializedFrame.Timestamp)
	assert.Equal(t, original.SourcePathId, deserializedFrame.SourcePathId)
	assert.Equal(t, original.TargetPathId, deserializedFrame.TargetPathId)

	// Deserialize the nested payload
	deserializedAddPathReq := &AddPathRequest{}
	err = proto.Unmarshal(deserializedFrame.Payload, deserializedAddPathReq)
	require.NoError(t, err)

	// Verify nested message fields
	assert.Equal(t, addPathReq.TargetAddress, deserializedAddPathReq.TargetAddress)
	assert.Equal(t, addPathReq.SessionId, deserializedAddPathReq.SessionId)
	assert.Equal(t, addPathReq.Priority, deserializedAddPathReq.Priority)
	assert.Equal(t, addPathReq.Metadata, deserializedAddPathReq.Metadata)
}