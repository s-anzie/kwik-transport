package session

import (
	"fmt"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/data"
	"kwik/pkg/stream"
	"kwik/proto/control"

	"google.golang.org/protobuf/proto"
)

// AuthenticationManager handles authentication for KWIK sessions
type AuthenticationManager struct {
	sessionID       string
	isClient        bool
	isAuthenticated bool
	credentials     []byte
	clientVersion   string
	serverVersion   string
	enabledFeatures []string
	sessionTimeout  time.Duration
	createdAt       time.Time
	sessionRole     control.SessionRole // Role of this session (PRIMARY or SECONDARY)

	logger stream.StreamLogger
}

// NewAuthenticationManager creates a new authentication manager
func NewAuthenticationManager(sessionID string, isClient bool) *AuthenticationManager {
	return &AuthenticationManager{
		sessionID:       sessionID,
		isClient:        isClient,
		isAuthenticated: false,
		credentials:     nil,
		clientVersion:   utils.KwikVersion,
		serverVersion:   "",
		enabledFeatures: utils.GetDefaultEnabledFeatures(),
		sessionTimeout:  utils.DefaultSessionTimeout,
		createdAt:       time.Now(),
	}
}

// SetLogger sets the logger for the authentication manager
func (am *AuthenticationManager) SetLogger(l stream.StreamLogger) { am.logger = l }

// CreateAuthenticationRequest creates an authentication request frame
func (am *AuthenticationManager) CreateAuthenticationRequest(role control.SessionRole) (*control.ControlFrame, error) {
	// Create authentication request
	authReq := &control.AuthenticationRequest{
		SessionId:         am.sessionID,
		Credentials:       am.credentials,
		ClientVersion:     am.clientVersion,
		SupportedFeatures: utils.GetSupportedFeatures(),
		Role:              role,
	}

	// Serialize request
	payload, err := proto.Marshal(authReq)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrSerializationFailed,
			"failed to serialize authentication request", err)
	}

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:   data.GenerateFrameID(),
		Type:      control.ControlFrameType_AUTHENTICATION_REQUEST,
		Payload:   payload,
		Timestamp: uint64(time.Now().UnixNano()),
	}

	return frame, nil
}

// HandleAuthenticationResponse handles an authentication response frame
func (am *AuthenticationManager) HandleAuthenticationResponse(frame *control.ControlFrame) error {
	if frame.Type != control.ControlFrameType_AUTHENTICATION_RESPONSE {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"expected authentication response frame", nil)
	}

	// Deserialize response
	var authResp control.AuthenticationResponse
	err := proto.Unmarshal(frame.Payload, &authResp)
	if err != nil {
		return utils.NewKwikError(utils.ErrDeserializationFailed,
			"failed to deserialize authentication response", err)
	}

	// Check authentication result
	if !authResp.Success {
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			authResp.ErrorMessage, nil)
	}

	// Validate session ID
	if authResp.SessionId != am.sessionID {
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			"session ID mismatch in authentication response", nil)
	}

	// Update authentication state
	am.isAuthenticated = true
	am.serverVersion = authResp.ServerVersion
	am.enabledFeatures = authResp.EnabledFeatures
	if authResp.SessionTimeout > 0 {
		am.sessionTimeout = time.Duration(authResp.SessionTimeout) * time.Second
	}

	return nil
}

// HandleAuthenticationRequest handles an authentication request frame (server-side)
func (am *AuthenticationManager) HandleAuthenticationRequest(frame *control.ControlFrame) (*control.ControlFrame, error) {
	if frame.Type != control.ControlFrameType_AUTHENTICATION_REQUEST {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame,
			"expected authentication request frame", nil)
	}

	// Deserialize request
	var authReq control.AuthenticationRequest
	err := proto.Unmarshal(frame.Payload, &authReq)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrDeserializationFailed,
			"failed to deserialize authentication request", err)
	}

	// Validate session role and ID based on authentication type
	success := true
	errorMessage := ""

	// Handle role-based authentication logic
	if am.logger != nil {
		am.logger.Debug(fmt.Sprintf("AuthManager handling authentication request with role: %v, sessionID: %s, isAuthenticated: %t",
			authReq.Role, authReq.SessionId, am.isAuthenticated))
	}

	if authReq.Role == control.SessionRole_PRIMARY {
		// Primary path authentication - this is the main session
		if am.isAuthenticated {
			// Already authenticated, this shouldn't happen for primary
			success = false
			errorMessage = "primary session already authenticated"
		} else {
			// Accept the session ID from client for primary authentication
			am.sessionID = authReq.SessionId
			am.sessionRole = control.SessionRole_PRIMARY
		}
	} else if authReq.Role == control.SessionRole_SECONDARY {
		// Secondary path authentication - this is a secondary server accepting a client connection
		// The secondary server should accept the client's session ID and role
		if !am.isAuthenticated {
			// First authentication on this secondary server - accept the session ID
			if am.logger != nil {
				am.logger.Debug(fmt.Sprintf("AuthManager accepting SECONDARY authentication for session %s", authReq.SessionId))
			}
			am.sessionID = authReq.SessionId
			am.sessionRole = control.SessionRole_SECONDARY
		} else if authReq.SessionId != am.sessionID {
			success = false
			errorMessage = "session ID mismatch for secondary path"
			if am.logger != nil {
				am.logger.Debug(fmt.Sprintf("AuthManager rejecting SECONDARY auth - session ID mismatch: expected %s, got %s",
					am.sessionID, authReq.SessionId))
			}
		} else {
			// Valid secondary path authentication with matching session ID
			if am.logger != nil {
				am.logger.Debug(fmt.Sprintf("AuthManager accepting SECONDARY authentication for existing session %s", authReq.SessionId))
			}
			am.sessionRole = control.SessionRole_SECONDARY
		}
	} else {
		success = false
		errorMessage = "invalid session role"
	}

	if am.logger != nil {
		am.logger.Debug(fmt.Sprintf("AuthManager authentication result: success=%t, errorMessage=%s, finalRole=%v",
			success, errorMessage, am.sessionRole))
	}

	// Validate client version compatibility
	if success && !am.isVersionCompatible(authReq.ClientVersion) {
		success = false
		errorMessage = "incompatible client version"
	}

	// Update authentication state if successful
	if success {
		am.isAuthenticated = true
		am.clientVersion = authReq.ClientVersion
		am.enabledFeatures = am.filterSupportedFeatures(authReq.SupportedFeatures)
	}

	// Create authentication response
	authResp := &control.AuthenticationResponse{
		Success:         success,
		SessionId:       am.sessionID,
		ErrorMessage:    errorMessage,
		ServerVersion:   utils.KwikVersion,
		EnabledFeatures: am.enabledFeatures,
		SessionTimeout:  uint64(am.sessionTimeout.Seconds()),
	}

	// Serialize response
	payload, err := proto.Marshal(authResp)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrSerializationFailed,
			"failed to serialize authentication response", err)
	}

	// Create response frame
	responseFrame := &control.ControlFrame{
		FrameId:   data.GenerateFrameID(),
		Type:      control.ControlFrameType_AUTHENTICATION_RESPONSE,
		Payload:   payload,
		Timestamp: uint64(time.Now().UnixNano()),
	}

	return responseFrame, nil
}

// IsAuthenticated returns whether the session is authenticated
func (am *AuthenticationManager) IsAuthenticated() bool {
	return am.isAuthenticated
}

// MarkAuthenticated marks the session as authenticated (for demo/testing purposes)
func (am *AuthenticationManager) MarkAuthenticated() {
	am.isAuthenticated = true
}

// GetSessionID returns the session ID
func (am *AuthenticationManager) GetSessionID() string {
	return am.sessionID
}

// GetEnabledFeatures returns the enabled features for this session
func (am *AuthenticationManager) GetEnabledFeatures() []string {
	return am.enabledFeatures
}

// GetSessionTimeout returns the session timeout
func (am *AuthenticationManager) GetSessionTimeout() time.Duration {
	return am.sessionTimeout
}

// GetSessionRole returns the role of this session
func (am *AuthenticationManager) GetSessionRole() control.SessionRole {
	return am.sessionRole
}

// SetSessionRole sets the role of this session
func (am *AuthenticationManager) SetSessionRole(role control.SessionRole) {
	am.sessionRole = role
}

// IsPrimarySession returns true if this is a primary session
func (am *AuthenticationManager) IsPrimarySession() bool {
	return am.sessionRole == control.SessionRole_PRIMARY
}

// IsSecondarySession returns true if this is a secondary session
func (am *AuthenticationManager) IsSecondarySession() bool {
	return am.sessionRole == control.SessionRole_SECONDARY
}

// isVersionCompatible checks if the client/server versions are compatible
func (am *AuthenticationManager) isVersionCompatible(version string) bool {
	// Simple version compatibility check
	// In a real implementation, this would do proper semantic version comparison
	return version == utils.KwikVersion || version == "1.0.0"
}

// filterSupportedFeatures filters the requested features to only include supported ones
func (am *AuthenticationManager) filterSupportedFeatures(requestedFeatures []string) []string {
	var enabledFeatures []string

	for _, feature := range requestedFeatures {
		if utils.IsFeatureSupported(feature) {
			enabledFeatures = append(enabledFeatures, feature)
		}
	}

	// Ensure we have at least the default features
	if len(enabledFeatures) == 0 {
		enabledFeatures = utils.GetDefaultEnabledFeatures()
	}

	return enabledFeatures
}
