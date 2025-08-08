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