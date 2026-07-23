package natstransport

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/RobertWHurst/zephyr"
	"github.com/nats-io/nats.go"
)

const DefaultMaxConcurrentHandlers = 256

type NatsTransport struct {
	NatsConnection    *nats.Conn
	dispatchHandlerWg sync.WaitGroup
	handlerSem        chan struct{}
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
		handlerSem:     make(chan struct{}, maxConcurrentHandlers),
	}
}

func signalReady(ready chan<- struct{}) {
	if ready == nil {
		return
	}
	select {
	case ready <- struct{}{}:
	default:
	}
}

func flushWithContext(ctx context.Context, conn *nats.Conn) error {
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.FlushWithContext(flushCtx); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	return nil
}
