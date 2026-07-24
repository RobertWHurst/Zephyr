package natstransport

import (
	"context"

	"github.com/RobertWHurst/zephyr/v2"
	"github.com/nats-io/nats.go"
	"github.com/telemetrytv/trace"
	"github.com/vmihailenco/msgpack/v5"
)

var (
	transportNatsAnnounceDebug = trace.Bind("zephyr:transport:nats:announce")
)

func (t *NatsTransport) AnnounceGateway(gatewayDescriptor *zephyr.GatewayDescriptor) error {
	transportNatsAnnounceDebug.Tracef("Announcing gateway %s with %d services",
		gatewayDescriptor.Name, len(gatewayDescriptor.ServiceDescriptors))

	descriptorBuf, err := msgpack.Marshal(gatewayDescriptor)
	if err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to marshal gateway descriptor: %v", err)
		return err
	}

	gatewayAnnounceSubject := namespace("gateway.announce")
	if err := t.NatsConnection.Publish(gatewayAnnounceSubject, descriptorBuf); err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to publish gateway announcement: %v", err)
		return err
	}

	return nil
}

func (t *NatsTransport) SubscribeGatewayAnnouncements(ctx context.Context, handler func(gatewayDescriptor *zephyr.GatewayDescriptor)) (zephyr.Subscription, error) {
	transportNatsAnnounceDebug.Trace("Subscribing to gateway announcements")

	return t.subscribeAsync(ctx, namespace("gateway.announce"), func(msg *nats.Msg) {
		gatewayDescriptor := &zephyr.GatewayDescriptor{}
		if err := msgpack.Unmarshal(msg.Data, gatewayDescriptor); err != nil {
			transportNatsAnnounceDebug.Tracef("Discarding malformed gateway announcement: %v", err)
			return
		}

		transportNatsAnnounceDebug.Tracef("Received announcement from gateway %s with %d services",
			gatewayDescriptor.Name, len(gatewayDescriptor.ServiceDescriptors))
		handler(gatewayDescriptor)
	})
}
