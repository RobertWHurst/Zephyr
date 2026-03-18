package zephyr

import (
	"net/http"
)

type Transport interface {
	AnnounceGateway(gatewayDescriptor *GatewayDescriptor) error
	BindGatewayAnnounce(handler func(gatewayDescriptor *GatewayDescriptor)) error
	UnbindGatewayAnnounce() error

	AnnounceService(serviceDescriptor *ServiceDescriptor) error
	BindServiceAnnounce(handler func(serviceDescriptor *ServiceDescriptor)) error
	UnbindServiceAnnounce() error

	Dispatch(serviceName string, res http.ResponseWriter, req *http.Request) error
	BindDispatch(serviceName string, handler func(res http.ResponseWriter, req *http.Request)) error
	UnbindDispatch(serviceName string) error

}
