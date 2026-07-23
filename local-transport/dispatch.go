package localtransport

import (
	"context"
	"net/http"

	"github.com/RobertWHurst/zephyr"
)

func (t *LocalTransport) Dispatch(serviceName string, responseWriter http.ResponseWriter, request *http.Request) error {
	transportLocalDispatchDebug.Tracef("Dispatching request to service %s: %s %s",
		serviceName, request.Method, request.URL.Path)

	t.mu.RLock()
	handler, ok := t.dispatchHandlers[serviceName]
	t.mu.RUnlock()
	if ok {
		transportLocalDispatchDebug.Tracef("Found handler for service %s, calling handler", serviceName)
		handler(responseWriter, request)
		transportLocalDispatchDebug.Tracef("Handler for service %s completed", serviceName)
	} else {
		transportLocalDispatchDebug.Tracef("No handler found for service %s", serviceName)
	}

	return nil
}

// SubscribeDispatch subscribes a service instance to dispatched requests.
func (t *LocalTransport) SubscribeDispatch(_ context.Context, serviceName string, handler func(responseWriter http.ResponseWriter, request *http.Request)) (zephyr.Subscription, error) {
	transportLocalDispatchDebug.Tracef("Subscribing to dispatch for service %s", serviceName)
	t.mu.Lock()
	t.dispatchHandlers[serviceName] = handler
	t.mu.Unlock()

	return untilDone(func() {
		transportLocalDispatchDebug.Tracef("Unsubscribing from dispatch for service %s", serviceName)
		t.mu.Lock()
		delete(t.dispatchHandlers, serviceName)
		t.mu.Unlock()
	}), nil
}
