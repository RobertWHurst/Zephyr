package natstransport

import (
	"sync"

	"github.com/RobertWHurst/zephyr"
	"github.com/nats-io/nats.go"
)

const DefaultMaxConcurrentHandlers = 256

type NatsTransport struct {
	NatsConnection        *nats.Conn
	unbindDispatch        map[string][]func() error
	unbindServiceAnnounce func() error
	unbindGatewayAnnounce func() error
	dispatchHandlerWg     sync.WaitGroup
	handlerSem            chan struct{}
}

var _ zephyr.Transport = &NatsTransport{}

func New(natsConnection *nats.Conn) *NatsTransport {
	return NewWithMaxConcurrency(natsConnection, DefaultMaxConcurrentHandlers)
}

func NewWithMaxConcurrency(natsConnection *nats.Conn, maxConcurrentHandlers int) *NatsTransport {
	if maxConcurrentHandlers <= 0 {
		maxConcurrentHandlers = DefaultMaxConcurrentHandlers
	}
	return &NatsTransport{
		NatsConnection: natsConnection,
		unbindDispatch: map[string][]func() error{},
		handlerSem:     make(chan struct{}, maxConcurrentHandlers),
	}
}
