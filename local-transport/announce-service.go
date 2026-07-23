package localtransport

import (
	"context"

	"github.com/RobertWHurst/zephyr"
)

func (c *LocalTransport) AnnounceService(serviceDescriptor *zephyr.ServiceDescriptor) error {
	transportLocalAnnounceDebug.Tracef("Announcing service %s with %d routes",
		serviceDescriptor.Name, len(serviceDescriptor.RouteDescriptors))

	c.mu.RLock()
	handlerCount := len(c.serviceAnnounceHandlers)
	handlers := make([]func(serviceDescriptor *zephyr.ServiceDescriptor), 0, len(c.serviceAnnounceHandlers))
	for _, handler := range c.serviceAnnounceHandlers {
		handlers = append(handlers, handler)
	}
	c.mu.RUnlock()
	transportLocalAnnounceDebug.Tracef("Notifying %d service announcement handlers", handlerCount)

	for _, handler := range handlers {
		handler(serviceDescriptor)
	}

	transportLocalAnnounceDebug.Trace("Service announcement completed")
	return nil
}

func (c *LocalTransport) HandleServiceAnnouncements(ctx context.Context, ready chan<- struct{}, handler func(serviceDescriptor *zephyr.ServiceDescriptor)) error {
	transportLocalAnnounceDebug.Trace("Handling service announcements")
	c.mu.Lock()
	id := c.registerHandler()
	c.serviceAnnounceHandlers[id] = handler
	transportLocalAnnounceDebug.Tracef("Now have %d service announcement handlers", len(c.serviceAnnounceHandlers))
	c.mu.Unlock()
	signalReady(ready)

	<-ctx.Done()
	c.mu.Lock()
	transportLocalAnnounceDebug.Tracef("Unbinding %d service announcement handlers", len(c.serviceAnnounceHandlers))
	delete(c.serviceAnnounceHandlers, id)
	c.mu.Unlock()
	return nil
}
