package transport

import "errors"

var (
	// ErrUnsupportedTransport is returned when an unsupported transport type is requested.
	ErrUnsupportedTransport = errors.New("unsupported transport type")
)
