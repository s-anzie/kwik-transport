package utils

import (
	"errors"
	"fmt"
)

// KWIK error codes
const (
	ErrPathNotFound           = "KWIK_PATH_NOT_FOUND"
	ErrPathDead              = "KWIK_PATH_DEAD"
	ErrInvalidFrame          = "KWIK_INVALID_FRAME"
	ErrAuthenticationFailed  = "KWIK_AUTH_FAILED"
	ErrStreamCreationFailed  = "KWIK_STREAM_CREATE_FAILED"
	ErrPacketTooLarge        = "KWIK_PACKET_TOO_LARGE"
	ErrOffsetMismatch        = "KWIK_OFFSET_MISMATCH"
	ErrSerializationFailed   = "KWIK_SERIALIZATION_FAILED"
	ErrDeserializationFailed = "KWIK_DESERIALIZATION_FAILED"
	ErrConnectionLost        = "KWIK_CONNECTION_LOST"
)

// KwikError represents a KWIK-specific error
type KwikError struct {
	Code    string
	Message string
	Cause   error
}

func (e *KwikError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s (caused by: %v)", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *KwikError) Unwrap() error {
	return e.Cause
}

// NewKwikError creates a new KWIK error
func NewKwikError(code, message string, cause error) *KwikError {
	return &KwikError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

// Common error constructors
func NewPathNotFoundError(pathID string) error {
	return NewKwikError(ErrPathNotFound, fmt.Sprintf("path %s not found", pathID), nil)
}

func NewPathDeadError(pathID string) error {
	return NewKwikError(ErrPathDead, fmt.Sprintf("path %s is dead", pathID), nil)
}

func NewInvalidFrameError(reason string) error {
	return NewKwikError(ErrInvalidFrame, reason, nil)
}

func NewAuthenticationFailedError(reason string) error {
	return NewKwikError(ErrAuthenticationFailed, reason, nil)
}

func NewStreamCreationFailedError(streamID uint64, cause error) error {
	return NewKwikError(ErrStreamCreationFailed, 
		fmt.Sprintf("failed to create stream %d", streamID), cause)
}

func NewPacketTooLargeError(size, maxSize uint32) error {
	return NewKwikError(ErrPacketTooLarge, 
		fmt.Sprintf("packet size %d exceeds maximum %d", size, maxSize), nil)
}

func NewOffsetMismatchError(expected, actual uint64) error {
	return NewKwikError(ErrOffsetMismatch, 
		fmt.Sprintf("offset mismatch: expected %d, got %d", expected, actual), nil)
}

// IsKwikError checks if an error is a KWIK error with specific code
func IsKwikError(err error, code string) bool {
	var kwikErr *KwikError
	if errors.As(err, &kwikErr) {
		return kwikErr.Code == code
	}
	return false
}