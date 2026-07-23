package zephyr

import (
	"context"
	"net/http"
)

// Subscription is a live transport stream created by one of the Subscribe
// methods on Transport. By the time a Subscription is returned the stream is
// registered with the transport backend — messages published from this moment
// on will be observed.
//
// Serve pumps the stream, invoking the handler passed to Subscribe. It must be
// called exactly once, blocks until the given context is canceled (returning
// nil) or the stream fails (returning the error), and releases the stream's
// resources before returning.
type Subscription interface {
	Serve(ctx context.Context) error
}

// SubscriptionFunc adapts a function to the Subscription interface.
type SubscriptionFunc func(ctx context.Context) error

func (f SubscriptionFunc) Serve(ctx context.Context) error { return f(ctx) }

// Transport moves announcements and dispatched HTTP requests between gateways
// and services. Handlers may be invoked concurrently.
type Transport interface {
	// AnnounceGateway broadcasts a gateway descriptor to all services.
	AnnounceGateway(gatewayDescriptor *GatewayDescriptor) error
	// SubscribeGatewayAnnouncements subscribes to gateway announcements.
	SubscribeGatewayAnnouncements(ctx context.Context, handler func(gatewayDescriptor *GatewayDescriptor)) (Subscription, error)

	// AnnounceService broadcasts a service descriptor to all gateways.
	AnnounceService(serviceDescriptor *ServiceDescriptor) error
	// SubscribeServiceAnnouncements subscribes to service announcements.
	SubscribeServiceAnnouncements(ctx context.Context, handler func(serviceDescriptor *ServiceDescriptor)) (Subscription, error)

	// Dispatch forwards an HTTP request to an instance of the named service
	// and writes the response to res.
	Dispatch(serviceName string, res http.ResponseWriter, req *http.Request) error
	// SubscribeDispatch subscribes a service instance to dispatched requests.
	SubscribeDispatch(ctx context.Context, serviceName string, handler func(res http.ResponseWriter, req *http.Request)) (Subscription, error)
}
