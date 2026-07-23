package localtransport

import (
	"context"

	"github.com/RobertWHurst/zephyr"
)

func (c *LocalTransport) AnnounceGateway(gatewayDescriptor *zephyr.GatewayDescriptor) error {
	transportLocalAnnounceDebug.Tracef("Announcing gateway %s with %d services",
		gatewayDescriptor.Name, len(gatewayDescriptor.ServiceDescriptors))

	c.mu.RLock()
	handlerCount := len(c.gatewayAnnounceHandlers)
	handlers := make([]func(gatewayDescriptor *zephyr.GatewayDescriptor), 0, len(c.gatewayAnnounceHandlers))
	for _, handler := range c.gatewayAnnounceHandlers {
		handlers = append(handlers, handler)
	}
	c.mu.RUnlock()
	transportLocalAnnounceDebug.Tracef("Notifying %d gateway announcement handlers", handlerCount)

	for _, handler := range handlers {
		handler(gatewayDescriptor)
	}

	transportLocalAnnounceDebug.Trace("Gateway announcement completed")
	return nil
}

func (c *LocalTransport) HandleGatewayAnnouncements(ctx context.Context, ready chan<- struct{}, handler func(gatewayDescriptor *zephyr.GatewayDescriptor)) error {
	transportLocalAnnounceDebug.Trace("Handling gateway announcements")
	c.mu.Lock()
	id := c.registerHandler()
	c.gatewayAnnounceHandlers[id] = handler
	transportLocalAnnounceDebug.Tracef("Now have %d gateway announcement handlers", len(c.gatewayAnnounceHandlers))
	c.mu.Unlock()
	signalReady(ready)

	<-ctx.Done()
	c.mu.Lock()
	transportLocalAnnounceDebug.Tracef("Unbinding %d gateway announcement handlers", len(c.gatewayAnnounceHandlers))
	delete(c.gatewayAnnounceHandlers, id)
	c.mu.Unlock()
	return nil
}
