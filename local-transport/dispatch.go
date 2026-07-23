package localtransport

import (
	"context"
	"net/http"
)

func (c *LocalTransport) Dispatch(serviceName string, responseWriter http.ResponseWriter, request *http.Request) error {
	transportLocalDispatchDebug.Tracef("Dispatching request to service %s: %s %s",
		serviceName, request.Method, request.URL.Path)

	c.mu.RLock()
	handler, ok := c.dispatchHandlers[serviceName]
	c.mu.RUnlock()
	if ok {
		transportLocalDispatchDebug.Tracef("Found handler for service %s, calling handler", serviceName)
		handler(responseWriter, request)
		transportLocalDispatchDebug.Tracef("Handler for service %s completed", serviceName)
	} else {
		transportLocalDispatchDebug.Tracef("No handler found for service %s", serviceName)
	}

	return nil
}

func (c *LocalTransport) HandleDispatch(ctx context.Context, ready chan<- struct{}, serviceName string, handler func(responseWriter http.ResponseWriter, request *http.Request)) error {
	transportLocalDispatchDebug.Tracef("Handling dispatch for service %s", serviceName)
	c.mu.Lock()
	c.dispatchHandlers[serviceName] = handler
	c.mu.Unlock()
	signalReady(ready)

	<-ctx.Done()
	transportLocalDispatchDebug.Tracef("Unbinding dispatch handler for service %s", serviceName)
	c.mu.Lock()
	delete(c.dispatchHandlers, serviceName)
	c.mu.Unlock()
	return nil
}
