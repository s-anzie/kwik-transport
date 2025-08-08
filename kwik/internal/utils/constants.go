package utils

import "time"

// Protocol constants
const (
	// KWIK protocol version
	ProtocolVersion = 1
	KwikVersion     = "1.0.0"
	
	// Default timeouts
	DefaultDialTimeout        = 30 * time.Second
	DefaultHandshakeTimeout   = 10 * time.Second
	DefaultKeepAliveInterval  = 30 * time.Second
	DefaultSessionTimeout     = 24 * time.Hour
	
	// Stream multiplexing constants
	OptimalLogicalStreamsPerReal = 4
	MaxLogicalStreamsPerReal     = 8
	MinLogicalStreamsPerReal     = 2
	
	// Packet size constants
	DefaultMaxPacketSize = 1200
	ProtobufOverhead     = 64  // Estimated protobuf overhead
	KwikHeaderSize       = 32  // Estimated KWIK header size
	
	// Buffer sizes
	DefaultReadBufferSize  = 4096
	DefaultWriteBufferSize = 4096
	
	// Path management
	MaxPaths              = 16
	PathHealthCheckInterval = 5 * time.Second
	DeadPathTimeout       = 30 * time.Second
)

// Frame type constants
const (
	ControlPlaneStreamID = 0
	DataPlaneStreamIDStart = 1
)
// Feature constants
var (
	// Default enabled features
	DefaultEnabledFeatures = []string{
		"multipath",
		"aggregation", 
		"stream_multiplexing",
		"path_migration",
		"raw_packets",
	}
	
	// All supported features
	SupportedFeatures = []string{
		"multipath",
		"aggregation",
		"stream_multiplexing", 
		"path_migration",
		"raw_packets",
		"compression",
		"encryption",
		"metrics",
		"debugging",
	}
)

// GetSupportedFeatures returns the list of supported KWIK features
func GetSupportedFeatures() []string {
	// Return a copy to prevent modification
	features := make([]string, len(SupportedFeatures))
	copy(features, SupportedFeatures)
	return features
}

// GetDefaultEnabledFeatures returns the list of default enabled features
func GetDefaultEnabledFeatures() []string {
	// Return a copy to prevent modification
	features := make([]string, len(DefaultEnabledFeatures))
	copy(features, DefaultEnabledFeatures)
	return features
}

// IsFeatureSupported checks if a feature is supported
func IsFeatureSupported(feature string) bool {
	for _, supported := range SupportedFeatures {
		if supported == feature {
			return true
		}
	}
	return false
}