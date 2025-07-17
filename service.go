package zephyr

import (
	"fmt"
	"net/http"

	"github.com/RobertWHurst/navaros"
	"github.com/telemetrytv/trace"
)

var (
	serviceDebug         = trace.Bind("zephyr:service")
	serviceAnnounceDebug = trace.Bind("zephyr:service:announce")
	serviceHandleDebug   = trace.Bind("zephyr:service:handler")
)

// Service is a struct that facilitates communication between a go microservice
// and a zephyr gateway. It will manage the announcement of the service to the
// gateway as well as calls any HTTP Handler or Navaros handler.
//
// If the service is given a Navaros router, it will automatically announce
// any public routes declared on the router. That said any handler compatible
// with go's http.HandlerFunc or http.Handler interface can be used.
//
// Note that if you opt to use something other than Navaros, you will need to
// assign your route descriptors manually. This can be done by using the
// zephyr.NewRouteDescriptor function to create your route descriptors, then
// assigning them to the RouteDescriptors field on the service.
type Service struct {

	// GatewayNames is a list of gateway names that the service should announce
	// itself to. If the list is empty, the service will announce itself to all
	// gateways on the connection.
	GatewayNames []string

	// Name is the name of the service. This is used to identify the service
	// when announcing it to the gateway.
	Name string

	// Transport is a struct that implements the Transport interface and
	// facilitates communication between services, gateways, and clients.
	Transport Transport

	// RouteDescriptors is a list of route descriptors that describe the routes
	// that this service can handle. If this is left empty, the service will
	// not be routable. This is automatically populated if the handler is a
	// Navaros router.
	RouteDescriptors []*RouteDescriptor

	// Handler is called when a request is made to the service. This can be
	// either a Navaros router or a standard http.Handler or http.HandlerFunc.
	Handler any

	stopChan chan struct{}
}

// NewService creates a new service with the given name, connection, and handler.
// The service will automatically announce itself to the gateway when it starts.
// If the handler is a Navaros router, the service will automatically announce
// any public routes declared on the router.
func NewService(name string, transport Transport, handler any) *Service {
	return &Service{
		Name:      name,
		Transport: transport,
		Handler:   handler,
		stopChan:  make(chan struct{}),
	}
}

// Start starts the service. This will bind the service to the connection and
// announce the service to the gateway. If the service is already bound to the
// connection, this will return an error. Start must be called before the
// service will receive requests.
func (s *Service) Start() error {
	serviceDebug.Tracef("Starting service %s", s.Name)

	if s.Transport == nil {
		serviceDebug.Trace("Transport not provided")
		return fmt.Errorf(
			"cannot start local service. The associated gateway already handles " +
				"incoming requests",
		)
	}

	serviceDebug.Trace("Binding gateway announcement handler")
	err := s.Transport.BindGatewayAnnounce(func(gatewayDescriptor *GatewayDescriptor) {
		s.handleGatewayAnnounce(gatewayDescriptor)
	})
	if err != nil {
		serviceDebug.Tracef("Failed to bind gateway announcement handler: %v", err)
		return err
	}

	serviceDebug.Trace("Binding dispatch handler")
	err = s.Transport.BindDispatch(s.Name, func(res http.ResponseWriter, req *http.Request) {
		serviceHandleDebug.Tracef("Handling request %s %s", req.Method, req.URL.Path)

		ctx := navaros.NewContext(res, req, s.Handler)
		ctx.Next()
		navaros.CtxFinalize(ctx)
		navaros.CtxFree(ctx)

		serviceHandleDebug.Tracef("Completed handling request %s %s", req.Method, req.URL.Path)
	})
	if err != nil {
		serviceDebug.Tracef("Failed to bind dispatch handler: %v", err)
		return err
	}

	serviceDebug.Trace("Announcing service to gateways")
	if err := s.doAnnounce(); err != nil {
		serviceDebug.Tracef("Failed to announce service: %v", err)
		return err
	}

	serviceDebug.Tracef("Service %s started successfully", s.Name)
	return nil
}

// Stop stops the service. This will unbind the service from the connection.
// This provides a way to dispose of the service if need be.
func (s *Service) Stop() {
	serviceDebug.Tracef("Stopping service %s", s.Name)

	serviceDebug.Trace("Unbinding gateway announcement handler")
	if err := s.Transport.UnbindGatewayAnnounce(); err != nil {
		serviceDebug.Tracef("Failed to unbind gateway announcement handler: %v", err)
		panic(err)
	}

	serviceDebug.Trace("Unbinding dispatch handler")
	if err := s.Transport.UnbindDispatch(s.Name); err != nil {
		serviceDebug.Tracef("Failed to unbind dispatch handler: %v", err)
		panic(err)
	}

	serviceDebug.Trace("Closing stop channel")
	close(s.stopChan)

	serviceDebug.Tracef("Service %s stopped successfully", s.Name)
}

func (s *Service) handleGatewayAnnounce(gatewayDescriptor *GatewayDescriptor) {
	serviceAnnounceDebug.Tracef("Received gateway announcement from %s", gatewayDescriptor.Name)

	isWantedGateway := len(s.GatewayNames) == 0
	if !isWantedGateway {
		for _, name := range s.GatewayNames {
			if name == gatewayDescriptor.Name {
				isWantedGateway = true
				break
			}
		}
	}
	if !isWantedGateway {
		serviceAnnounceDebug.Tracef("Ignoring announcement from unwanted gateway %s", gatewayDescriptor.Name)
		return
	}

	serviceAnnounceDebug.Trace("Checking if service is registered with gateway")
	foundSelf := false
	for _, descriptor := range gatewayDescriptor.ServiceDescriptors {
		if descriptor.Name == s.Name {
			serviceAnnounceDebug.Trace("Service found in gateway's service index")
			foundSelf = true
			break
		}
	}

	if !foundSelf {
		serviceAnnounceDebug.Trace("Service not found in gateway's service index, announcing service")
		if err := s.doAnnounce(); err != nil {
			serviceAnnounceDebug.Tracef("Failed to announce service: %v", err)
			panic(err)
		}
	}
}

// Run starts the service and blocks until the service is stopped.
func (s *Service) Run() error {
	serviceDebug.Tracef("Running service %s", s.Name)

	if err := s.Start(); err != nil {
		serviceDebug.Tracef("Failed to start service: %v", err)
		return err
	}

	serviceDebug.Trace("Waiting for service to be stopped")
	<-s.stopChan

	serviceDebug.Trace("Service run completed")
	return nil
}

func (s *Service) doAnnounce() error {
	serviceAnnounceDebug.Tracef("Service %s announcing to gateways", s.Name)

	routeDescriptors := s.RouteDescriptors

	if routeDescriptors == nil {
		if h, ok := s.Handler.(navaros.RouterHandler); ok {
			serviceAnnounceDebug.Trace("Extracting route descriptors from Navaros router")
			navarosRouteDescriptors := h.RouteDescriptors()
			for _, navarosRouteDescriptor := range navarosRouteDescriptors {
				routeDescriptors = append(routeDescriptors, &RouteDescriptor{
					Method:  string(navarosRouteDescriptor.Method),
					Pattern: navarosRouteDescriptor.Pattern,
				})
			}
		}
	}

	if len(routeDescriptors) > 0 {
		serviceAnnounceDebug.Tracef("Announcing %d routes", len(routeDescriptors))
		for _, route := range routeDescriptors {
			serviceAnnounceDebug.Tracef("Route: %s %s", route.Method, route.Pattern)
		}
	} else {
		serviceAnnounceDebug.Trace("No routes to announce")
	}

	return s.Transport.AnnounceService(&ServiceDescriptor{
		Name:             s.Name,
		GatewayNames:     s.GatewayNames,
		RouteDescriptors: routeDescriptors,
	})
}
