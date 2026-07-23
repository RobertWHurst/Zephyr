package natstransport

import (
	"context"

	"github.com/RobertWHurst/zephyr"
	"github.com/nats-io/nats.go"
	"github.com/vmihailenco/msgpack/v5"
)

func (t *NatsTransport) AnnounceService(serviceDescriptor *zephyr.ServiceDescriptor) error {
	transportNatsAnnounceDebug.Tracef("Announcing service %s with %d routes",
		serviceDescriptor.Name, len(serviceDescriptor.RouteDescriptors))

	serviceDescriptorBuf, err := msgpack.Marshal(serviceDescriptor)
	if err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to marshal service descriptor: %v", err)
		return err
	}

	serviceAnnounceSubject := namespace("service.announce")
	if err := t.NatsConnection.Publish(serviceAnnounceSubject, serviceDescriptorBuf); err != nil {
		transportNatsAnnounceDebug.Tracef("Failed to publish service announcement: %v", err)
		return err
	}

	return nil
}

func (t *NatsTransport) SubscribeServiceAnnouncements(ctx context.Context, handler func(serviceDescriptor *zephyr.ServiceDescriptor)) (zephyr.Subscription, error) {
	transportNatsAnnounceDebug.Trace("Subscribing to service announcements")

	return t.subscribeAsync(ctx, namespace("service.announce"), func(msg *nats.Msg) {
		serviceDescriptor := &zephyr.ServiceDescriptor{}
		if err := msgpack.Unmarshal(msg.Data, serviceDescriptor); err != nil {
			transportNatsAnnounceDebug.Tracef("Discarding malformed service announcement: %v", err)
			return
		}

		transportNatsAnnounceDebug.Tracef("Received announcement from service %s with %d routes",
			serviceDescriptor.Name, len(serviceDescriptor.RouteDescriptors))
		handler(serviceDescriptor)
	})
}
