package localtransport

import (
	"context"

	"github.com/RobertWHurst/zephyr/v2"
)

func (t *LocalTransport) AnnounceService(serviceDescriptor *zephyr.ServiceDescriptor) error {
	transportLocalAnnounceDebug.Tracef("Announcing service %s with %d routes",
		serviceDescriptor.Name, len(serviceDescriptor.RouteDescriptors))

	t.mu.RLock()
	handlers := make([]func(serviceDescriptor *zephyr.ServiceDescriptor), 0, len(t.serviceAnnounceHandlers))
	for _, handler := range t.serviceAnnounceHandlers {
		handlers = append(handlers, handler)
	}
	t.mu.RUnlock()

	transportLocalAnnounceDebug.Tracef("Notifying %d service announcement handlers", len(handlers))
	for _, handler := range handlers {
		handler(serviceDescriptor)
	}

	return nil
}

func (t *LocalTransport) SubscribeServiceAnnouncements(_ context.Context, handler func(serviceDescriptor *zephyr.ServiceDescriptor)) (zephyr.Subscription, error) {
	transportLocalAnnounceDebug.Trace("Subscribing to service announcements")
	t.mu.Lock()
	id := t.registerHandlerID()
	t.serviceAnnounceHandlers[id] = handler
	t.mu.Unlock()

	return untilDone(func() {
		transportLocalAnnounceDebug.Trace("Unsubscribing from service announcements")
		t.mu.Lock()
		delete(t.serviceAnnounceHandlers, id)
		t.mu.Unlock()
	}), nil
}
