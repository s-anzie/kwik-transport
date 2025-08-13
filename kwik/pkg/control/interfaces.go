package control

import (
	"kwik/pkg/protocol"
)

// ControlPlane manages control plane operations
type ControlPlane interface {
	SendFrame(pathID string, frame protocol.Frame) error
	ReceiveFrame(pathID string) (protocol.Frame, error)
	HandleAddPathRequest(req *AddPathRequest) error
	HandleRemovePathRequest(req *RemovePathRequest) error
	HandleAuthenticationRequest(req *AuthenticationRequest) error
	HandleRawPacketTransmission(req *RawPacketTransmission) error
	SendPathStatusNotification(pathID string, status PathStatus) error
}

// CommandHandler handles specific control commands
type CommandHandler interface {
	HandleCommand(frame protocol.Frame) error
}

// NotificationSender sends notifications to peers
type NotificationSender interface {
	SendNotification(pathID string, notification Notification) error
}

// Control message types
type AddPathRequest struct {
	TargetAddress string
	SessionID     string
}

type AddPathResponse struct {
	Success      bool
	PathID       string
	ErrorMessage string
}

type RemovePathRequest struct {
	PathID string
}

type RemovePathResponse struct {
	Success      bool
	ErrorMessage string
}

type AuthenticationRequest struct {
	SessionID   string
	Credentials []byte
}

type AuthenticationResponse struct {
	Success     bool
	SessionID   string
	ErrorMessage string
}

type PathStatusNotification struct {
	PathID string
	Status PathStatus
	Reason string
}

type StreamCreateNotification struct {
	LogicalStreamID uint64
	PathID          string
}

type RawPacketTransmission struct {
	Data           []byte
	TargetPathID   string
	SourceServerID string
	ProtocolHint   string
	PreserveOrder  bool
}

// PathStatus represents path status in control messages
type PathStatus int

const (
	PathStatusActive PathStatus = iota
	PathStatusDead
	PathStatusConnecting
	PathStatusDisconnecting
)

// Notification represents a generic notification
type Notification interface {
	Type() string
	Serialize() ([]byte, error)
}

// SecondaryStreamNotificationHandler handles secondary stream control messages
type SecondaryStreamNotificationHandler interface {
	OnSecondaryStreamMappingUpdate(pathID string, mapping *SecondaryStreamMapping) error
	OnOffsetSyncRequest(pathID string, request *OffsetSyncRequest) error
	OnOffsetSyncResponse(pathID string, response *OffsetSyncResponse) error
	OnSecondaryStreamError(pathID string, error *SecondaryStreamError) error
}

// Secondary stream control message types
type SecondaryStreamMapping struct {
	SecondaryStreamID uint64
	KwikStreamID      uint64
	PathID            string
	Operation         MappingOperation
	Timestamp         uint64
}

type OffsetSyncRequest struct {
	KwikStreamID      uint64
	CurrentOffset     uint64
	RequestingPathID  string
	Timestamp         uint64
}

type OffsetSyncResponse struct {
	KwikStreamID   uint64
	ExpectedOffset uint64
	SyncRequired   bool
	Timestamp      uint64
}

type SecondaryStreamError struct {
	Code             string
	Message          string
	PathID           string
	StreamID         uint64
	KwikStreamID     uint64
	Timestamp        uint64
}

type MappingOperation int

const (
	MappingOperationCreate MappingOperation = iota
	MappingOperationUpdate
	MappingOperationDelete
)