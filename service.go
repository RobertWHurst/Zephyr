package zephyr

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/RobertWHurst/navaros"
	"github.com/telemetryos/go-debug/debug"
	"golang.org/x/sync/errgroup"
)

var (
	serviceDebug         = debug.Bind("zephyr:service")
	serviceAnnounceDebug = debug.Bind("zephyr:service:announce")
	serviceHandleDebug   = debug.Bind("zephyr:service:handler")
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

	lifecycle lifecycle
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
	}
}

// Listen starts the service and blocks until the context is canceled, Close
// is called, or a transport stream fails. A service may listen again after a
// clean shutdown.
func (s *Service) Listen(ctx context.Context) error {
	serviceDebug.Tracef("Listening as service %s", s.Name)

	if s.Transport == nil {
		serviceDebug.Trace("Transport not provided")
		return ErrNoTransport
	}

	listenCtx, err := s.lifecycle.begin(ctx, ErrServiceAlreadyListening)
	if err != nil {
		serviceDebug.Trace("Service already listening")
		return err
	}
	defer s.lifecycle.end()

	grp, grpCtx := errgroup.WithContext(listenCtx)
	finish := func(err error) error {
		s.lifecycle.interrupt()
		if waitErr := grp.Wait(); waitErr != nil && err == nil {
			err = waitErr
		}
		return err
	}

	streams := []struct {
		name      string
		subscribe func(context.Context) (Subscription, error)
	}{
		{"gateway announcements", func(ctx context.Context) (Subscription, error) {
			return s.Transport.SubscribeGatewayAnnouncements(ctx, s.handleGatewayAnnounce)
		}},
		{"dispatch", func(ctx context.Context) (Subscription, error) {
			return s.Transport.SubscribeDispatch(ctx, s.Name, s.handleDispatch)
		}},
	}
	for _, stream := range streams {
		sub, err := stream.subscribe(grpCtx)
		if err != nil {
			return finish(fmt.Errorf("%s: %w", stream.name, err))
		}
		name := stream.name
		grp.Go(func() error {
			if err := sub.Serve(grpCtx); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			return nil
		})
	}

	serviceDebug.Trace("Announcing service to gateways")
	if err := s.doAnnounce(); err != nil {
		serviceDebug.Tracef("Failed to announce service: %v", err)
		return finish(err)
	}

	s.lifecycle.markReady()
	return finish(grp.Wait())
}

// Close interrupts a running Listen call and waits for it to return. It is
// safe to call multiple times, and is a no-op when the service is not
// listening.
func (s *Service) Close() error {
	serviceDebug.Tracef("Closing service %s", s.Name)
	s.lifecycle.close()
	return nil
}

// Ready returns a channel that is closed once the current Listen call has
// subscribed its transport streams and announced the service. It is intended
// for startup sequencing and readiness probes.
func (s *Service) Ready() <-chan struct{} {
	return s.lifecycle.readyChan()
}

func (s *Service) handleDispatch(res http.ResponseWriter, req *http.Request) {
	serviceHandleDebug.Tracef("Handling request %s %s", req.Method, req.URL.Path)

	ctx := navaros.NewContext(res, req, s.Handler)
	ctx.Next()
	navaros.CtxFinalize(ctx)
	navaros.CtxFree(ctx)

	serviceHandleDebug.Tracef("Completed handling request %s %s", req.Method, req.URL.Path)
}

func (s *Service) handleGatewayAnnounce(gatewayDescriptor *GatewayDescriptor) {
	serviceAnnounceDebug.Tracef("Received gateway announcement from %s", gatewayDescriptor.Name)

	isWantedGateway := len(s.GatewayNames) == 0 ||
		slices.Contains(s.GatewayNames, gatewayDescriptor.Name)
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
		}
	}
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
					Method:   string(navarosRouteDescriptor.Method),
					Pattern:  navarosRouteDescriptor.Pattern,
					Metadata: navarosRouteDescriptor.Metadata,
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
