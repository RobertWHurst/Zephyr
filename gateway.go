package zephyr

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"github.com/RobertWHurst/navaros"
	"github.com/telemetryos/go-debug/debug"
	"golang.org/x/sync/errgroup"
)

var (
	gatewayDebug        = debug.Bind("zephyr:gateway")
	gatewayRouteDebug   = debug.Bind("zephyr:gateway:route")
	gatewayIndexerDebug = debug.Bind("zephyr:gateway:indexer")
)

// GatewayAnnounceInterval is the default interval between gateway
// announcements. It is jittered per process so a fleet of gateways does not
// announce in lockstep.
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

	AnnounceInterval time.Duration

	gsi atomic.Pointer[GatewayServiceIndexer]

	lifecycle lifecycle
}

var _ http.Handler = &Gateway{}
var _ navaros.Handler = &Gateway{}

func NewGateway(name string, transport Transport) *Gateway {
	return &Gateway{
		Name:             name,
		Transport:        transport,
		AnnounceInterval: GatewayAnnounceInterval,
	}
}

// Connect joins the gateway to the transport and blocks until the context is
// canceled, Close is called, or a transport stream fails. A gateway may be
// connected again after a clean shutdown.
func (g *Gateway) Connect(ctx context.Context) error {
	gatewayDebug.Tracef("Connecting gateway %s", g.Name)

	if g.Transport == nil {
		gatewayDebug.Trace("Transport not provided")
		return ErrNoTransport
	}

	connectCtx, err := g.lifecycle.begin(ctx, ErrGatewayAlreadyConnected)
	if err != nil {
		gatewayDebug.Trace("Gateway already connected")
		return err
	}
	defer g.lifecycle.end()

	gatewayIndexerDebug.Trace("Initializing service indexer")
	gsi := &GatewayServiceIndexer{}
	g.gsi.Store(gsi)
	defer gsi.Close()

	grp, grpCtx := errgroup.WithContext(connectCtx)
	finish := func(err error) error {
		g.lifecycle.interrupt()
		if waitErr := grp.Wait(); waitErr != nil && err == nil {
			err = waitErr
		}
		return err
	}

	announceSub, err := g.Transport.SubscribeServiceAnnouncements(grpCtx, func(serviceDescriptor *ServiceDescriptor) {
		g.handleServiceAnnouncement(gsi, serviceDescriptor)
	})
	if err != nil {
		return finish(fmt.Errorf("service announcements: %w", err))
	}
	grp.Go(func() error {
		if err := announceSub.Serve(grpCtx); err != nil {
			return fmt.Errorf("service announcements: %w", err)
		}
		return nil
	})

	grp.Go(func() error {
		g.announceLoop(grpCtx, gsi)
		return nil
	})
	grp.Go(func() error {
		g.pruneLoop(grpCtx, gsi)
		return nil
	})

	gatewayDebug.Tracef("Announcing gateway %s", g.Name)
	if err := g.Transport.AnnounceGateway(&GatewayDescriptor{
		Name:               g.Name,
		ServiceDescriptors: gsi.Descriptors(),
	}); err != nil {
		return finish(err)
	}

	g.lifecycle.markReady()
	return finish(grp.Wait())
}

// Close interrupts a running Connect call and waits for it to return. It is
// safe to call multiple times, and is a no-op when the gateway is not
// connected.
func (g *Gateway) Close() error {
	gatewayDebug.Tracef("Closing gateway %s", g.Name)
	g.lifecycle.close()
	return nil
}

// Ready returns a channel that is closed once the current Connect call has
// subscribed its transport streams and announced the gateway. It is intended
// for startup sequencing and readiness probes.
func (g *Gateway) Ready() <-chan struct{} {
	return g.lifecycle.readyChan()
}

func (g *Gateway) handleServiceAnnouncement(gsi *GatewayServiceIndexer, serviceDescriptor *ServiceDescriptor) {
	gatewayIndexerDebug.Tracef("Received service announcement from %s", serviceDescriptor.Name)

	announcingToThisGateway := len(serviceDescriptor.GatewayNames) == 0 ||
		slices.Contains(serviceDescriptor.GatewayNames, g.Name)
	if !announcingToThisGateway {
		gatewayIndexerDebug.Tracef("Service %s not announcing to this gateway", serviceDescriptor.Name)
		return
	}

	gatewayIndexerDebug.Tracef("Indexing service %s with %d routes",
		serviceDescriptor.Name, len(serviceDescriptor.RouteDescriptors))
	if err := gsi.SetServiceDescriptor(serviceDescriptor); err != nil {
		gatewayIndexerDebug.Tracef("Failed to index service %s: %v", serviceDescriptor.Name, err)
	}
}

func (g *Gateway) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	gatewayRouteDebug.Tracef("Received HTTP request %s %s", req.Method, req.URL.Path)

	gsi := g.gsi.Load()
	if gsi == nil {
		gatewayRouteDebug.Trace("Gateway not connected, returning 503")
		res.WriteHeader(503)
		return
	}

	sd, _, ok := gsi.ResolveService(req.Method, req.URL.Path)
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

	gsi := g.gsi.Load()
	if gsi == nil {
		gatewayRouteDebug.Trace("Gateway not connected, cannot serve request")
		return false
	}

	_, _, ok := gsi.ResolveService(req.Method, req.URL.Path)
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

	gsi := g.gsi.Load()
	if gsi == nil {
		gatewayRouteDebug.Trace("Gateway not connected, skipping to next handler")
		ctx.Next()
		return
	}

	sd, rd, ok := gsi.ResolveService(string(method), path)
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

	gsi := g.gsi.Load()
	if gsi == nil {
		gatewayRouteDebug.Trace("Gateway not connected, cannot handle request")
		return false
	}

	_, _, ok := gsi.ResolveService(string(method), path)
	if ok {
		gatewayRouteDebug.Tracef("Can handle %s %s", method, path)
	} else {
		gatewayRouteDebug.Tracef("Cannot handle %s %s, no matching service", method, path)
	}
	return ok
}

func (g *Gateway) announceLoop(ctx context.Context, gsi *GatewayServiceIndexer) {
	ticker := time.NewTicker(g.AnnounceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Only include fresh services in the announcement. Stale services
			// are excluded so they don't see themselves in the list — this
			// prompts them to re-announce if still alive.
			fresh := gsi.FreshServiceDescriptors(3 * g.AnnounceInterval)
			gatewayDebug.Tracef("Periodic announce for gateway %s (%d fresh services)", g.Name, len(fresh))
			if err := g.Transport.AnnounceGateway(&GatewayDescriptor{
				Name:               g.Name,
				ServiceDescriptors: fresh,
			}); err != nil {
				gatewayDebug.Tracef("Failed to announce gateway: %v", err)
			}
		}
	}
}

func (g *Gateway) pruneLoop(ctx context.Context, gsi *GatewayServiceIndexer) {
	ticker := time.NewTicker(g.AnnounceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			gsi.PruneStaleServices(5 * g.AnnounceInterval)
		}
	}
}

// DescriptorMiddleware returns a navaros middleware that resolves the
// matching route descriptor for the incoming request and sets it on the
// context. Downstream middleware can retrieve it with
// RouteDescriptorFromContext.
func (g *Gateway) DescriptorMiddleware() navaros.HandlerFunc {
	return func(ctx *navaros.Context) {
		if gsi := g.gsi.Load(); gsi != nil {
			ctx.Set(ContextKeyServiceDescriptors, gsi.Descriptors())

			method := string(ctx.Method())
			path := ctx.Path()
			if sd, rd, ok := gsi.ResolveService(method, path); ok && rd != nil {
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
