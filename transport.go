package zephyr

import (
	"context"
	"net/http"
)

type Transport interface {
	AnnounceGateway(gatewayDescriptor *GatewayDescriptor) error
	HandleGatewayAnnouncements(ctx context.Context, ready chan<- struct{}, handler func(gatewayDescriptor *GatewayDescriptor)) error

	AnnounceService(serviceDescriptor *ServiceDescriptor) error
	HandleServiceAnnouncements(ctx context.Context, ready chan<- struct{}, handler func(serviceDescriptor *ServiceDescriptor)) error

	Dispatch(serviceName string, res http.ResponseWriter, req *http.Request) error
	HandleDispatch(ctx context.Context, ready chan<- struct{}, serviceName string, handler func(res http.ResponseWriter, req *http.Request)) error
}
