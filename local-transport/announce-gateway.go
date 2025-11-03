package localtransport

import "github.com/RobertWHurst/zephyr"

func (c *LocalTransport) AnnounceGateway(gatewayDescriptor *zephyr.GatewayDescriptor) error {
	transportLocalAnnounceDebug.Tracef("Announcing gateway %s with %d services",
		gatewayDescriptor.Name, len(gatewayDescriptor.ServiceDescriptors))

	handlerCount := len(c.gatewayAnnounceHandlers)
	transportLocalAnnounceDebug.Tracef("Notifying %d gateway announcement handlers", handlerCount)

	for _, handler := range c.gatewayAnnounceHandlers {
		handler(gatewayDescriptor)
	}

	transportLocalAnnounceDebug.Trace("Gateway announcement completed")
	return nil
}

func (c *LocalTransport) BindGatewayAnnounce(handler func(gatewayDescriptor *zephyr.GatewayDescriptor)) error {
	transportLocalAnnounceDebug.Trace("Binding gateway announcement handler")
	c.gatewayAnnounceHandlers = append(c.gatewayAnnounceHandlers, handler)
	transportLocalAnnounceDebug.Tracef("Now have %d gateway announcement handlers", len(c.gatewayAnnounceHandlers))
	return nil
}

func (c *LocalTransport) UnbindGatewayAnnounce() error {
	transportLocalAnnounceDebug.Tracef("Unbinding %d gateway announcement handlers", len(c.gatewayAnnounceHandlers))
	c.gatewayAnnounceHandlers = nil
	transportLocalAnnounceDebug.Trace("All gateway announcement handlers unbound")
	return nil
}
