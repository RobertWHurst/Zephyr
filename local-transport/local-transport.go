package localtransport

import (
	"context"
	"net/http"
	"sync"

	"github.com/RobertWHurst/zephyr/v2"
	"github.com/telemetryos/go-debug/debug"
)

var (
	transportLocalDebug         = debug.Bind("zephyr:transport:local")
	transportLocalDispatchDebug = debug.Bind("zephyr:transport:local:dispatch")
	transportLocalAnnounceDebug = debug.Bind("zephyr:transport:local:announce")
)

// LocalTransport is an in-process transport for development and testing.
// Handlers run synchronously on the caller's goroutine.
type LocalTransport struct {
	mu                      sync.RWMutex
	nextHandlerID           uint64
	gatewayAnnounceHandlers map[uint64]func(gatewayDescriptor *zephyr.GatewayDescriptor)
	serviceAnnounceHandlers map[uint64]func(serviceDescriptor *zephyr.ServiceDescriptor)
	dispatchHandlers        map[string]func(responseWriter http.ResponseWriter, request *http.Request)
}

var _ zephyr.Transport = &LocalTransport{}

func New() *LocalTransport {
	transportLocalDebug.Trace("Creating new local transport")
	return &LocalTransport{
		gatewayAnnounceHandlers: map[uint64]func(gatewayDescriptor *zephyr.GatewayDescriptor){},
		serviceAnnounceHandlers: map[uint64]func(serviceDescriptor *zephyr.ServiceDescriptor){},
		dispatchHandlers:        map[string]func(responseWriter http.ResponseWriter, request *http.Request){},
	}
}

// registerHandlerID must be called with t.mu held.
func (t *LocalTransport) registerHandlerID() uint64 {
	t.nextHandlerID++
	return t.nextHandlerID
}

// untilDone returns a Subscription that blocks until the serve context is
// canceled, then runs unregister.
func untilDone(unregister func()) zephyr.Subscription {
	return zephyr.SubscriptionFunc(func(ctx context.Context) error {
		<-ctx.Done()
		unregister()
		return nil
	})
}
