package natstransport

import (
	"context"
	"sync"
	"time"

	"github.com/RobertWHurst/zephyr"
	"github.com/nats-io/nats.go"
)

const DefaultMaxConcurrentHandlers = 256

const flushTimeout = 5 * time.Second

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

// flush confirms the server has processed everything sent so far, bounding
// the wait with flushTimeout and the given context.
func (t *NatsTransport) flush(ctx context.Context) error {
	flushCtx, cancel := context.WithTimeout(ctx, flushTimeout)
	defer cancel()
	return t.NatsConnection.FlushWithContext(flushCtx)
}

// subscribeAsync subscribes with an async handler and returns a Subscription
// that unsubscribes when its serve context is canceled.
func (t *NatsTransport) subscribeAsync(ctx context.Context, subject string, cb nats.MsgHandler) (zephyr.Subscription, error) {
	sub, err := t.NatsConnection.Subscribe(subject, cb)
	if err != nil {
		return nil, err
	}
	if err := t.flush(ctx); err != nil {
		_ = sub.Unsubscribe()
		return nil, err
	}
	return zephyr.SubscriptionFunc(func(ctx context.Context) error {
		<-ctx.Done()
		if err := sub.Unsubscribe(); err != nil {
			transportNatsDebug.Tracef("Failed to unsubscribe from %s during shutdown: %v", subject, err)
		}
		return nil
	}), nil
}
