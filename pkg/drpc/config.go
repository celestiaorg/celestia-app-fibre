package drpc

// DRPCConfig defines the configuration for the DRPC server.
// This configuration is stored in app.toml and follows the same pattern
// as the gRPC configuration in the Cosmos SDK.
type DRPCConfig struct {
	// Enable defines if the DRPC server should be enabled.
	Enable bool `mapstructure:"enable"`

	// Address defines the DRPC server address to bind to.
	// Format: "host:port" (e.g., "0.0.0.0:26658")
	Address string `mapstructure:"address"`

	// MaxRecvMsgSize defines the max message size in bytes the server can receive.
	// The default value is 256MB, which is suitable for large blob data transfers.
	MaxRecvMsgSize int `mapstructure:"max-recv-msg-size"`

	// MaxSendMsgSize defines the max message size in bytes the server can send.
	// The default value is 256MB, which is suitable for large blob data transfers.
	MaxSendMsgSize int `mapstructure:"max-send-msg-size"`
}

// DefaultDRPCConfig returns the default DRPC server configuration.
func DefaultDRPCConfig() DRPCConfig {
	return DRPCConfig{
		Enable:         true,
		Address:        "0.0.0.0:26658",
		MaxRecvMsgSize: 256 * 1024 * 1024, // 256MB
		MaxSendMsgSize: 256 * 1024 * 1024, // 256MB
	}
}
