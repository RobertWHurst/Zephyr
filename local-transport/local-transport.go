package localtransport

import (
	"net/http"
	"sync"

	"github.com/RobertWHurst/zephyr"
	"github.com/telemetrytv/trace"
)

var (
	transportLocalDebug         = trace.Bind("zephyr:transport:local")
	transportLocalDispatchDebug = trace.Bind("zephyr:transport:local:dispatch")
	transportLocalAnnounceDebug = trace.Bind("zephyr:transport:local:announce")
)

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

func (c *LocalTransport) registerHandler() uint64 {
	c.nextHandlerID++
	return c.nextHandlerID
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
