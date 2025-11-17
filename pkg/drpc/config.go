package drpc

import (
	"time"

	"github.com/libp2p/go-yamux/v5"
)

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

	// FibreTransport defines which transport to use for the Fibre service.
	// Valid values: "grpc" or "drpc" (default: "drpc")
	FibreTransport string `mapstructure:"fibre-transport"`

	// MultiplexTransport defines which multiplexing transport to use for DRPC.
	// Valid values: "yamux" (default) or "quic"
	// This only applies when FibreTransport is "drpc".
	MultiplexTransport string `mapstructure:"multiplex-transport"`
}

// DefaultDRPCConfig returns the default DRPC server configuration.
func DefaultDRPCConfig() DRPCConfig {
	return DRPCConfig{
		Enable:             true,
		Address:            "0.0.0.0:26658",
		MaxRecvMsgSize:     256 * 1024 * 1024, // 256MB
		MaxSendMsgSize:     256 * 1024 * 1024, // 256MB
		FibreTransport:     "drpc",            // Default to DRPC for Fibre service
		MultiplexTransport: "yamux",           // Default to yamux for backward compatibility
	}
}

var YamuxCfg = yamux10GConfig()

func yamux10GConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()

	// --- concurrency limits ---
	cfg.MaxIncomingStreams = 128 // up to 128 concurrent inbound streams
	cfg.AcceptBacklog = 128      // at most 128 waiting to be accepted
	cfg.PingBacklog = 32

	// --- flow control (key for throughput vs memory) ---
	cfg.InitialStreamWindowSize = 16 * 1024 * 1024 // 4 MiB initial window
	cfg.MaxStreamWindowSize = 64 * 1024 * 1024     // 8 MiB max window

	// 16 streams * 8 MiB ~= 128 MiB max in-flight per connection (plus overhead).

	// --- message / buffer sizing ---
	cfg.MaxMessageSize = 4 * 1024 * 1024 // 512 KiB max frame; good for big chunks
	cfg.ReadBufSize = 4 * 1024 * 1024    // 1 MiB session read buffer

	// --- write coalescing / latency trade-off ---
	cfg.WriteCoalesceDelay = 100 * time.Microsecond // small batching window

	// --- keepalives / timeouts (mostly safety, not throughput) ---
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second
	cfg.MeasureRTTInterval = 10 * time.Second
	cfg.ConnectionWriteTimeout = 30 * time.Second

	// optional:
	// cfg.LogOutput = io.Discard // or your logger

	return cfg
}
