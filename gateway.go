package zephyr

import (
	"fmt"
	"math/rand"
	"net/http"
	"slices"
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

// ContextKeyServiceDescriptor is the key used to store the matched service
// descriptor on the navaros context.
const ContextKeyServiceDescriptor = "zephyr:service-descriptor"

// ContextKeyServiceDescriptors is the key used to store all service descriptors
// on the navaros context.
const ContextKeyServiceDescriptors = "zephyr:service-descriptors"

type Gateway struct {
	Name      string
	Transport Transport
	gsi       *GatewayServiceIndexer
	stopChan  chan struct{}
}

var _ http.Handler = &Gateway{}
var _ navaros.Handler = &Gateway{}

func NewGateway(name string, transport Transport) *Gateway {
	return &Gateway{
		Name:      name,
		Transport: transport,
		stopChan:  make(chan struct{}),
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
			if slices.Contains(serviceDescriptor.GatewayNames, g.Name) {
				announcingToThisGateway = true
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

	go g.pruneLoop()

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

	close(g.stopChan)

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

	sd, _, ok := g.gsi.ResolveService(req.Method, req.URL.Path)
	if !ok {
		gatewayRouteDebug.Tracef("No service found for %s %s, returning 404", req.Method, req.URL.Path)
		res.WriteHeader(404)
		return
	}

	gatewayRouteDebug.Tracef("Resolved %s %s to service %s", req.Method, req.URL.Path, sd.Name)

	if err := g.Transport.Dispatch(sd.Name, res, req); err != nil {
		gatewayRouteDebug.Tracef("Error dispatching to %s: %v", sd.Name, err)
		panic(fmt.Errorf("failed to dispatch request to %s: %w", sd.Name, err))
	}

	gatewayRouteDebug.Tracef("Successfully dispatched %s %s to %s", req.Method, req.URL.Path, sd.Name)
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

	sd, rd, ok := g.gsi.ResolveService(string(method), path)
	if !ok {
		gatewayRouteDebug.Tracef("No service found for %s %s, skipping to next handler", method, path)
		ctx.Next()
		return
	}

	gatewayRouteDebug.Tracef("Resolved %s %s to service %s", method, path, sd.Name)

	if rd != nil {
		ctx.Set(ContextKeyRouteDescriptor, rd)
	}

	// This Panic is ok because it will be caught and handled by Navaros
	if err := g.Transport.Dispatch(sd.Name, ctx.ResponseWriter(), ctx.Request()); err != nil {
		gatewayRouteDebug.Tracef("Error dispatching to %s: %v", sd.Name, err)
		panic(err)
	}

	gatewayRouteDebug.Tracef("Successfully dispatched %s %s to %s", method, path, sd.Name)
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

func (g *Gateway) pruneLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-g.stopChan:
			return
		case <-ticker.C:
			g.gsi.PruneStaleServices(15*time.Second, 30*time.Second)
		}
	}
}

// DescriptorMiddleware returns a navaros middleware that resolves the
// matching route descriptor for the incoming request and sets it on the
// context. Downstream middleware can retrieve it with
// RouteDescriptorFromContext.
func (g *Gateway) DescriptorMiddleware() navaros.HandlerFunc {
	return func(ctx *navaros.Context) {
		if g.gsi != nil {
			ctx.Set(ContextKeyServiceDescriptors, g.gsi.ServiceDescriptors)

			method := string(ctx.Method())
			path := ctx.Path()
			if sd, rd, ok := g.gsi.ResolveService(method, path); ok && rd != nil {
				ctx.Set(ContextKeyRouteDescriptor, rd)
				ctx.Set(ContextKeyServiceDescriptor, sd)
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

// ServiceDescriptorFromContext retrieves the service descriptor that was set on
// the context by DescriptorMiddleware or Handle. Returns nil if no descriptor
// was set.
func ServiceDescriptorFromContext(ctx *navaros.Context) *ServiceDescriptor {
	v, ok := ctx.Get(ContextKeyServiceDescriptor)
	if !ok {
		return nil
	}
	sd, _ := v.(*ServiceDescriptor)
	return sd
}

// ServiceDescriptorsFromContext retrieves the service descriptors that were set
// on the context by DescriptorMiddleware or Handle. Returns nil if no descriptors
// were set.
func ServiceDescriptorsFromContext(ctx *navaros.Context) []*ServiceDescriptor {
	v, ok := ctx.Get(ContextKeyServiceDescriptors)
	if !ok {
		return nil
	}
	sds, _ := v.([]*ServiceDescriptor)
	return sds
}
