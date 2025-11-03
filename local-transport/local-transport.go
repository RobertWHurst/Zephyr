package localtransport

import (
	"net/http"

	"github.com/RobertWHurst/zephyr"
	"github.com/telemetrytv/trace"
)

var (
	transportLocalDebug         = trace.Bind("zephyr:transport:local")
	transportLocalDispatchDebug = trace.Bind("zephyr:transport:local:dispatch")
	transportLocalAnnounceDebug = trace.Bind("zephyr:transport:local:announce")
)

type LocalTransport struct {
	gatewayAnnounceHandlers []func(gatewayDescriptor *zephyr.GatewayDescriptor)
	serviceAnnounceHandlers []func(serviceDescriptor *zephyr.ServiceDescriptor)
	dispatchHandlers        map[string]func(responseWriter http.ResponseWriter, request *http.Request)
}

var _ zephyr.Transport = &LocalTransport{}

func New() *LocalTransport {
	transportLocalDebug.Trace("Creating new local transport")
	return &LocalTransport{
		dispatchHandlers: map[string]func(responseWriter http.ResponseWriter, request *http.Request){},
	}
}
