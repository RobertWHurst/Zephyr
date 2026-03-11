package zephyr

import (
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/RobertWHurst/navaros"
	"github.com/telemetrytv/trace"
)

var (
	gatewayDebug        = trace.Bind("zephyr:gateway")
	gatewayRouteDebug   = trace.Bind("zephyr:gateway:route")
	gatewayIndexerDebug = trace.Bind("zephyr:gateway:indexer")
)

var GatewayAnnounceInterval = time.Duration((8 + rand.Intn(2))) * time.Second

// ContextKeyRouteDescriptor is the key used to store the matched route
// descriptor on the navaros context.
const ContextKeyRouteDescriptor = "zephyr:route-descriptor"

type Gateway struct {
	Name      string
	Transport Transport
	gsi       *GatewayServiceIndexer
}

var _ http.Handler = &Gateway{}
var _ navaros.Handler = &Gateway{}

func NewGateway(name string, transport Transport) *Gateway {
	return &Gateway{
		Name:      name,
		Transport: transport,
	}
}

// Announce runs a loop which sends a message to all services periodically,
// introducing them to this gateway if they are not already aware of it.
// The announcement message contains information about the services that
// this gateway is aware of. Services that do not see themselves in the
// announcement message are expected to send a reply to the gateway with their
// routing information.
func (g *Gateway) Start() error {
	gatewayDebug.Tracef("Starting gateway %s", g.Name)

	if g.gsi != nil {
		gatewayDebug.Trace("Gateway already started")
		return fmt.Errorf("gateway already started")
	}

	gatewayIndexerDebug.Trace("Initializing service indexer")
	g.gsi = &GatewayServiceIndexer{
		ServiceDescriptors: []*ServiceDescriptor{},
	}

	if g.Transport == nil {
		gatewayDebug.Trace("Transport not provided")
		return fmt.Errorf(
			"cannot start local service. The associated gateway already handles " +
				"incoming requests",
		)
	}

	gatewayDebug.Trace("Binding service announcement handler")
	err := g.Transport.BindServiceAnnounce(func(serviceDescriptor *ServiceDescriptor) {
		gatewayIndexerDebug.Tracef("Received service announcement from %s", serviceDescriptor.Name)

		announcingToThisGateway := len(serviceDescriptor.GatewayNames) == 0
		if !announcingToThisGateway {
			for _, gatewayName := range serviceDescriptor.GatewayNames {
				if gatewayName == g.Name {
					announcingToThisGateway = true
					break
				}
			}
		}
		if !announcingToThisGateway {
			gatewayIndexerDebug.Tracef("Service %s not announcing to this gateway", serviceDescriptor.Name)
			return
		}

		gatewayIndexerDebug.Tracef("Indexing service %s with %d routes",
			serviceDescriptor.Name, len(serviceDescriptor.RouteDescriptors))
		if err := g.gsi.SetServiceDescriptor(serviceDescriptor); err != nil {
			gatewayIndexerDebug.Tracef("Failed to index service %s: %v", serviceDescriptor.Name, err)
			panic(err)
		}
	})
	if err != nil {
		gatewayDebug.Tracef("Failed to bind service announcement handler: %v", err)
		return err
	}

	gatewayDebug.Tracef("Announcing gateway %s", g.Name)
	return g.Transport.AnnounceGateway(&GatewayDescriptor{
		Name:               g.Name,
		ServiceDescriptors: g.gsi.ServiceDescriptors,
	})
}

func (g *Gateway) Stop() {
	gatewayDebug.Tracef("Stopping gateway %s", g.Name)

	if g.gsi == nil || g.gsi.IsClosed() {
		gatewayDebug.Trace("Gateway already stopped")
		return
	}

	gatewayDebug.Trace("Closing service indexer")
	g.gsi.Close()

	gatewayDebug.Trace("Unbinding service announce handler")
	if err := g.Transport.UnbindServiceAnnounce(); err != nil {
		gatewayDebug.Tracef("Failed to unbind service announce: %v", err)
		panic(err)
	}

	gatewayDebug.Trace("Gateway stopped successfully")
}

func (g *Gateway) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	gatewayRouteDebug.Tracef("Received HTTP request %s %s", req.Method, req.URL.Path)

	if g.gsi == nil {
		gatewayRouteDebug.Trace("Gateway not started, returning 503")
		res.WriteHeader(503)
		return
	}

	serviceName, _, ok := g.gsi.ResolveService(req.Method, req.URL.Path)
	if !ok {
		gatewayRouteDebug.Tracef("No service found for %s %s, returning 404", req.Method, req.URL.Path)
		res.WriteHeader(404)
		return
	}

	gatewayRouteDebug.Tracef("Resolved %s %s to service %s", req.Method, req.URL.Path, serviceName)

	if err := g.Transport.Dispatch(serviceName, res, req); err != nil {
		gatewayRouteDebug.Tracef("Error dispatching to %s: %v", serviceName, err)
		panic(fmt.Errorf("failed to dispatch request to %s: %w", serviceName, err))
	}

	gatewayRouteDebug.Tracef("Successfully dispatched %s %s to %s", req.Method, req.URL.Path, serviceName)
}

func (g *Gateway) CanServeHTTP(req *http.Request) bool {
	gatewayRouteDebug.Tracef("Checking if gateway can serve %s %s", req.Method, req.URL.Path)

	if g.gsi == nil {
		gatewayRouteDebug.Trace("Gateway not started, cannot serve request")
		return false
	}

	_, _, ok := g.gsi.ResolveService(req.Method, req.URL.Path)
	if ok {
		gatewayRouteDebug.Tracef("Can serve %s %s", req.Method, req.URL.Path)
	} else {
		gatewayRouteDebug.Tracef("Cannot serve %s %s, no matching service", req.Method, req.URL.Path)
	}
	return ok
}

func (g *Gateway) Handle(ctx *navaros.Context) {
	method := ctx.Method()
	path := ctx.Path()
	gatewayRouteDebug.Tracef("Received Navaros request %s %s", method, path)

	if g.gsi == nil {
		gatewayRouteDebug.Trace("Gateway not started, skipping to next handler")
		ctx.Next()
		return
	}

	serviceName, rd, ok := g.gsi.ResolveService(string(method), path)
	if !ok {
		gatewayRouteDebug.Tracef("No service found for %s %s, skipping to next handler", method, path)
		ctx.Next()
		return
	}

	gatewayRouteDebug.Tracef("Resolved %s %s to service %s", method, path, serviceName)

	if rd != nil {
		ctx.Set(ContextKeyRouteDescriptor, rd)
	}

	// This Panic is ok because it will be caught and handled by Navaros
	if err := g.Transport.Dispatch(serviceName, ctx.ResponseWriter(), ctx.Request()); err != nil {
		gatewayRouteDebug.Tracef("Error dispatching to %s: %v", serviceName, err)
		panic(err)
	}

	gatewayRouteDebug.Tracef("Successfully dispatched %s %s to %s", method, path, serviceName)
}

func (g *Gateway) CanHandle(ctx *navaros.Context) bool {
	method := ctx.Method()
	path := ctx.Path()
	gatewayRouteDebug.Tracef("Checking if gateway can handle Navaros request %s %s", method, path)

	if g.gsi == nil {
		gatewayRouteDebug.Trace("Gateway not started, cannot handle request")
		return false
	}

	_, _, ok := g.gsi.ResolveService(string(method), path)
	if ok {
		gatewayRouteDebug.Tracef("Can handle %s %s", method, path)
	} else {
		gatewayRouteDebug.Tracef("Cannot handle %s %s, no matching service", method, path)
	}
	return ok
}

// DescriptorMiddleware returns a navaros middleware that resolves the
// matching route descriptor for the incoming request and sets it on the
// context. Downstream middleware can retrieve it with
// RouteDescriptorFromContext.
func (g *Gateway) DescriptorMiddleware() navaros.HandlerFunc {
	return func(ctx *navaros.Context) {
		if g.gsi != nil {
			method := string(ctx.Method())
			path := ctx.Path()
			if _, rd, ok := g.gsi.ResolveService(method, path); ok && rd != nil {
				ctx.Set(ContextKeyRouteDescriptor, rd)
			}
		}
		ctx.Next()
	}
}

// DispatchMiddleware returns a navaros middleware that dispatches the
// request to the resolved service. This is a convenience wrapper around
// Handle for use with router.Use().
func (g *Gateway) DispatchMiddleware() navaros.HandlerFunc {
	return func(ctx *navaros.Context) {
		g.Handle(ctx)
	}
}

// RouteDescriptorFromContext retrieves the route descriptor that was set on
// the context by DescriptorMiddleware or Handle. Returns nil if no descriptor
// was set.
func RouteDescriptorFromContext(ctx *navaros.Context) *RouteDescriptor {
	v, ok := ctx.Get(ContextKeyRouteDescriptor)
	if !ok {
		return nil
	}
	rd, _ := v.(*RouteDescriptor)
	return rd
}
