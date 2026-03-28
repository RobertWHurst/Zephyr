package natstransport

import (
	"sync"

	"github.com/RobertWHurst/zephyr"
	"github.com/nats-io/nats.go"
)

type NatsTransport struct {
	NatsConnection        *nats.Conn
	unbindDispatch        map[string][]func() error
	unbindServiceAnnounce func() error
	unbindGatewayAnnounce func() error
	dispatchHandlerWg     sync.WaitGroup
}

var _ zephyr.Transport = &NatsTransport{}

func New(natsConnection *nats.Conn) *NatsTransport {
	return &NatsTransport{
		NatsConnection:  natsConnection,
		unbindDispatch: map[string][]func() error{},
	}
}
