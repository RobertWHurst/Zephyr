package zephyr

import (
	"context"
	"fmt"
	"net/http"
	"sync"

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

	lifecycleMu   sync.Mutex
	closeCancel   context.CancelFunc
	lifecycleDone chan struct{}
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

// Listen starts the service and blocks until the context is canceled, Close is
// called, or a required transport stream fails.
func (s *Service) Listen(ctx context.Context) error {
	serviceDebug.Tracef("Listening as service %s", s.Name)
	if s.Transport == nil {
		serviceDebug.Trace("Transport not provided")
		return fmt.Errorf(
			"cannot listen as local service. The associated gateway already handles " +
				"incoming requests",
		)
	}

	listenCtx, cancel := context.WithCancel(ctx)
	if err := s.setCloseCancel(cancel); err != nil {
		cancel()
		return err
	}
	defer close(s.lifecycleDone)
	defer s.clearCloseCancel()
	defer cancel()

	ready := make(chan struct{}, 2)
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	goRun := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				select {
				case errCh <- fmt.Errorf("%s: %w", name, err):
				case <-listenCtx.Done():
				}
			}
		}()
	}

	serviceDebug.Trace("Handling gateway announcements")
	goRun("gateway announcements", func() error {
		return s.Transport.HandleGatewayAnnouncements(listenCtx, ready, func(gatewayDescriptor *GatewayDescriptor) {
			s.handleGatewayAnnounce(listenCtx, gatewayDescriptor)
		})
	})

	serviceDebug.Trace("Handling dispatch")
	goRun("dispatch", func() error {
		return s.Transport.HandleDispatch(listenCtx, ready, s.Name, func(res http.ResponseWriter, req *http.Request) {
			serviceHandleDebug.Tracef("Handling request %s %s", req.Method, req.URL.Path)

			ctx := navaros.NewContext(res, req, s.Handler)
			ctx.Next()
			navaros.CtxFinalize(ctx)
			navaros.CtxFree(ctx)

			serviceHandleDebug.Tracef("Completed handling request %s %s", req.Method, req.URL.Path)
		})
	})

	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case err := <-errCh:
			cancel()
			wg.Wait()
			return err
		case <-listenCtx.Done():
			cancel()
			wg.Wait()
			return nil
		}
	}

	serviceDebug.Trace("Announcing service to gateways")
	if err := s.doAnnounce(); err != nil {
		cancel()
		wg.Wait()
		serviceDebug.Tracef("Failed to announce service: %v", err)
		return err
	}

	select {
	case err := <-errCh:
		cancel()
		wg.Wait()
		return err
	case <-listenCtx.Done():
		cancel()
		wg.Wait()
		return nil
	}
}

// Close interrupts a running Listen call. It is safe to call multiple times.
func (s *Service) Close() error {
	s.lifecycleMu.Lock()
	cancel := s.closeCancel
	done := s.lifecycleDone
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

func (s *Service) handleGatewayAnnounce(ctx context.Context, gatewayDescriptor *GatewayDescriptor) {
	serviceAnnounceDebug.Tracef("Received gateway announcement from %s", gatewayDescriptor.Name)
	if ctx.Err() != nil {
		return
	}

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

func (s *Service) setCloseCancel(cancel context.CancelFunc) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closeCancel != nil {
		return fmt.Errorf("service already listening")
	}
	s.closeCancel = cancel
	s.lifecycleDone = make(chan struct{})
	return nil
}

func (s *Service) clearCloseCancel() {
	s.lifecycleMu.Lock()
	s.closeCancel = nil
	s.lifecycleDone = nil
	s.lifecycleMu.Unlock()
}
