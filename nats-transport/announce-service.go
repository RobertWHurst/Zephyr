package natstransport

import (
	"github.com/RobertWHurst/zephyr"
	"github.com/nats-io/nats.go"
	"github.com/vmihailenco/msgpack/v5"
)

func (c *NatsTransport) AnnounceService(serviceDescriptor *zephyr.ServiceDescriptor) error {
	transportNatsAnnounceDebug.Tracef("Announcing service %s with %d routes",
		serviceDescriptor.Name, len(serviceDescriptor.RouteDescriptors))

	transportNatsAnnounceDebug.Trace("Marshaling service descriptor")
	serviceDescriptorBuf, err := msgpack.Marshal(serviceDescriptor)
	if err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to marshal service descriptor: %v", err)
		return err
	}

	serviceAnnounceSubject := namespace("service.announce")
	transportNatsAnnounceDebug.Tracef("Publishing service announcement to %s", serviceAnnounceSubject)

	if err := c.NatsConnection.Publish(serviceAnnounceSubject, serviceDescriptorBuf); err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to publish service announcement: %v", err)
		return err
	}

	transportNatsAnnounceDebug.Trace("Service announcement published successfully")
	return nil
}

func (c *NatsTransport) BindServiceAnnounce(handler func(serviceDescriptor *zephyr.ServiceDescriptor)) error {
	transportNatsAnnounceDebug.Trace("Binding service announcement handler")

	subHandler := func(msg *nats.Msg) {
		transportNatsAnnounceDebug.Trace("Received service announcement")

		serviceDescriptorBuf := msg.Data
		serviceDescriptor := &zephyr.ServiceDescriptor{}

		if err := msgpack.Unmarshal(serviceDescriptorBuf, serviceDescriptor); err != nil {
			transportNatsAnnounceDebug.Tracef("Failed to unmarshal service descriptor: %v", err)
			panic(err)
		}

		transportNatsAnnounceDebug.Tracef("Received announcement from service %s with %d routes",
			serviceDescriptor.Name, len(serviceDescriptor.RouteDescriptors))

		handler(serviceDescriptor)
	}

	serviceAnnounceSubject := namespace("service.announce")
	transportNatsAnnounceDebug.Tracef("Subscribing to service announcements on %s", serviceAnnounceSubject)

	serviceAnnounceSub, err := c.NatsConnection.Subscribe(serviceAnnounceSubject, subHandler)
	if err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to subscribe to service announcements: %v", err)
		return err
	}

	transportNatsAnnounceDebug.Trace("Successfully subscribed to service announcements")
	c.unbindServiceAnnounce = func() error {
		transportNatsAnnounceDebug.Trace("Unsubscribing from service announcements")
		return serviceAnnounceSub.Unsubscribe()
	}

	return nil
}

func (c *NatsTransport) UnbindServiceAnnounce() error {
	transportNatsAnnounceDebug.Trace("Unbinding service announcement handler")
	err := c.unbindServiceAnnounce()
	if err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to unbind service announcement handler: %v", err)
	} else {
		transportNatsAnnounceDebug.Trace("Successfully unbound service announcement handler")
	}
	return err
}
