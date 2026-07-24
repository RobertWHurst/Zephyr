package localtransport

import (
	"context"

	"github.com/RobertWHurst/zephyr/v2"
)

func (t *LocalTransport) AnnounceGateway(gatewayDescriptor *zephyr.GatewayDescriptor) error {
	transportLocalAnnounceDebug.Tracef("Announcing gateway %s with %d services",
		gatewayDescriptor.Name, len(gatewayDescriptor.ServiceDescriptors))

	t.mu.RLock()
	handlers := make([]func(gatewayDescriptor *zephyr.GatewayDescriptor), 0, len(t.gatewayAnnounceHandlers))
	for _, handler := range t.gatewayAnnounceHandlers {
		handlers = append(handlers, handler)
	}
	t.mu.RUnlock()

	transportLocalAnnounceDebug.Tracef("Notifying %d gateway announcement handlers", len(handlers))
	for _, handler := range handlers {
		handler(gatewayDescriptor)
	}

	return nil
}

func (t *LocalTransport) SubscribeGatewayAnnouncements(_ context.Context, handler func(gatewayDescriptor *zephyr.GatewayDescriptor)) (zephyr.Subscription, error) {
	transportLocalAnnounceDebug.Trace("Subscribing to gateway announcements")
	t.mu.Lock()
	id := t.registerHandlerID()
	t.gatewayAnnounceHandlers[id] = handler
	t.mu.Unlock()

	return untilDone(func() {
		transportLocalAnnounceDebug.Trace("Unsubscribing from gateway announcements")
		t.mu.Lock()
		delete(t.gatewayAnnounceHandlers, id)
		t.mu.Unlock()
	}), nil
}
