package natstransport

import (
	"github.com/RobertWHurst/zephyr"
	"github.com/nats-io/nats.go"
	"github.com/telemetrytv/trace"
	"github.com/vmihailenco/msgpack/v5"
)

var (
	transportNatsAnnounceDebug = trace.Bind("zephyr:transport:nats:announce")
)

func (c *NatsTransport) AnnounceGateway(gatewayDescriptor *zephyr.GatewayDescriptor) error {
	transportNatsAnnounceDebug.Tracef("Announcing gateway %s with %d services",
		gatewayDescriptor.Name, len(gatewayDescriptor.ServiceDescriptors))

	transportNatsAnnounceDebug.Trace("Marshaling gateway descriptor")
	descriptorBuf, err := msgpack.Marshal(gatewayDescriptor)
	if err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to marshal gateway descriptor: %v", err)
		return err
	}

	gatewayAnnounceSubject := namespace("gateway.announce")
	transportNatsAnnounceDebug.Tracef("Publishing gateway announcement to %s", gatewayAnnounceSubject)

	if err := c.NatsConnection.Publish(gatewayAnnounceSubject, descriptorBuf); err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to publish gateway announcement: %v", err)
		return err
	}

	transportNatsAnnounceDebug.Trace("Gateway announcement published successfully")
	return nil
}

func (c *NatsTransport) BindGatewayAnnounce(handler func(gatewayDescriptor *zephyr.GatewayDescriptor)) error {
	transportNatsAnnounceDebug.Trace("Binding gateway announcement handler")

	subHandler := func(msg *nats.Msg) {
		transportNatsAnnounceDebug.Trace("Received gateway announcement")

		gatewayDescriptorBuf := msg.Data
		gatewayDescriptor := &zephyr.GatewayDescriptor{}

		if err := msgpack.Unmarshal(gatewayDescriptorBuf, gatewayDescriptor); err != nil {
			transportNatsAnnounceDebug.Tracef("Failed to unmarshal gateway descriptor: %v", err)
			panic(err)
		}

		transportNatsAnnounceDebug.Tracef("Received announcement from gateway %s with %d services",
			gatewayDescriptor.Name, len(gatewayDescriptor.ServiceDescriptors))

		handler(gatewayDescriptor)
	}

	gatewayAnnounceSubject := namespace("gateway.announce")
	transportNatsAnnounceDebug.Tracef("Subscribing to gateway announcements on %s", gatewayAnnounceSubject)

	gatewayAnnounceSub, err := c.NatsConnection.Subscribe(gatewayAnnounceSubject, subHandler)
	if err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to subscribe to gateway announcements: %v", err)
		return err
	}

	transportNatsAnnounceDebug.Trace("Successfully subscribed to gateway announcements")
	c.unbindGatewayAnnounce = func() error {
		transportNatsAnnounceDebug.Trace("Unsubscribing from gateway announcements")
		return gatewayAnnounceSub.Unsubscribe()
	}

	return nil
}

func (c *NatsTransport) UnbindGatewayAnnounce() error {
	transportNatsAnnounceDebug.Trace("Unbinding gateway announcement handler")
	err := c.unbindGatewayAnnounce()
	if err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to unbind gateway announcement handler: %v", err)
	} else {
		transportNatsAnnounceDebug.Trace("Successfully unbound gateway announcement handler")
	}
	return err
}
