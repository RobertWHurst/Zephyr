package localtransport

import "github.com/RobertWHurst/zephyr"

func (c *LocalTransport) AnnounceService(serviceDescriptor *zephyr.ServiceDescriptor) error {
	transportLocalAnnounceDebug.Tracef("Announcing service %s with %d routes",
		serviceDescriptor.Name, len(serviceDescriptor.RouteDescriptors))

	handlerCount := len(c.serviceAnnounceHandlers)
	transportLocalAnnounceDebug.Tracef("Notifying %d service announcement handlers", handlerCount)

	for _, handler := range c.serviceAnnounceHandlers {
		handler(serviceDescriptor)
	}

	transportLocalAnnounceDebug.Trace("Service announcement completed")
	return nil
}

func (c *LocalTransport) BindServiceAnnounce(handler func(serviceDescriptor *zephyr.ServiceDescriptor)) error {
	transportLocalAnnounceDebug.Trace("Binding service announcement handler")
	c.serviceAnnounceHandlers = append(c.serviceAnnounceHandlers, handler)
	transportLocalAnnounceDebug.Tracef("Now have %d service announcement handlers", len(c.serviceAnnounceHandlers))
	return nil
}

func (c *LocalTransport) UnbindServiceAnnounce() error {
	transportLocalAnnounceDebug.Tracef("Unbinding %d service announcement handlers", len(c.serviceAnnounceHandlers))
	c.serviceAnnounceHandlers = nil
	transportLocalAnnounceDebug.Trace("All service announcement handlers unbound")
	return nil
}
