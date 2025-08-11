package control

import (
	"kwik/internal/utils"
	"kwik/pkg/protocol"
	"kwik/proto/control"
	"google.golang.org/protobuf/proto"
)

// RawPacketHandler handles raw packet transmission commands
type RawPacketHandler struct {
	controlPlane ControlPlane
}

// NewRawPacketHandler creates a new raw packet handler
func NewRawPacketHandler(controlPlane ControlPlane) *RawPacketHandler {
	return &RawPacketHandler{
		controlPlane: controlPlane,
	}
}

// HandleCommand handles raw packet transmission control frames
func (h *RawPacketHandler) HandleCommand(frame protocol.Frame) error {
	if frame.Type() != protocol.FrameTypeRawPacketTransmission {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"expected raw packet transmission frame", nil)
	}

	// Extract payload from frame
	payload := frame.(*ProtocolControlFrame).Payload

	// Deserialize RawPacketTransmission message
	var rawPacketMsg control.RawPacketTransmission
	err := proto.Unmarshal(payload, &rawPacketMsg)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to deserialize raw packet transmission", err)
	}

	// Convert to control message type
	req := &RawPacketTransmission{
		Data:           rawPacketMsg.Data,
		TargetPathID:   rawPacketMsg.TargetPathId,
		SourceServerID: rawPacketMsg.SourceServerId,
		ProtocolHint:   rawPacketMsg.ProtocolHint,
		PreserveOrder:  rawPacketMsg.PreserveOrder,
	}

	// Handle the raw packet transmission
	return h.controlPlane.HandleRawPacketTransmission(req)
}