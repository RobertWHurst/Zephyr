package zephyr

import "errors"

var (
	// ErrGatewayAlreadyConnected is returned by Gateway.Connect when the
	// gateway is already connected.
	ErrGatewayAlreadyConnected = errors.New("zephyr: gateway already connected")

	// ErrServiceAlreadyListening is returned by Service.Listen when the
	// service is already listening.
	ErrServiceAlreadyListening = errors.New("zephyr: service already listening")

	// ErrNoTransport is returned by Gateway.Connect and Service.Listen when no
	// transport was provided. Local services attached directly to a gateway
	// are driven by the gateway and must not be started on their own.
	ErrNoTransport = errors.New("zephyr: no transport provided")
)
