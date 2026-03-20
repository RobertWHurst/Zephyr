package zephyr_test

import (
	"testing"
	"time"

	"github.com/RobertWHurst/zephyr"
	localtransport "github.com/RobertWHurst/zephyr/local-transport"
	"github.com/stretchr/testify/assert"
)

func TestGatewayServiceIndexer_SetServiceDescriptor(t *testing.T) {
	t.Run("Adds new service and sets LastSeenAt", func(t *testing.T) {
		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{},
		}

		rd := mustRouteDescriptor("GET", "/test")
		err := gsi.SetServiceDescriptor(&zephyr.ServiceDescriptor{
			Name:             "test-service",
			RouteDescriptors: []*zephyr.RouteDescriptor{rd},
		})
		assert.NoError(t, err)
		assert.Len(t, gsi.ServiceDescriptors, 1)
		assert.Equal(t, "test-service", gsi.ServiceDescriptors[0].Name)
		assert.NotNil(t, gsi.ServiceDescriptors[0].LastSeenAt)
	})

	t.Run("Updates existing service and refreshes LastSeenAt", func(t *testing.T) {
		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{},
		}

		rd1 := mustRouteDescriptor("GET", "/old")
		err := gsi.SetServiceDescriptor(&zephyr.ServiceDescriptor{
			Name:             "test-service",
			RouteDescriptors: []*zephyr.RouteDescriptor{rd1},
		})
		assert.NoError(t, err)

		firstSeen := *gsi.ServiceDescriptors[0].LastSeenAt

		// Set LastSeenAt to the past to verify it gets refreshed
		past := time.Now().Add(-1 * time.Minute)
		gsi.ServiceDescriptors[0].LastSeenAt = &past

		rd2 := mustRouteDescriptor("GET", "/new")
		err = gsi.SetServiceDescriptor(&zephyr.ServiceDescriptor{
			Name:             "test-service",
			RouteDescriptors: []*zephyr.RouteDescriptor{rd2},
		})
		assert.NoError(t, err)

		assert.Len(t, gsi.ServiceDescriptors, 1)
		assert.True(t, gsi.ServiceDescriptors[0].LastSeenAt.After(firstSeen) ||
			gsi.ServiceDescriptors[0].LastSeenAt.Equal(firstSeen))
	})
}

func TestGatewayServiceIndexer_FreshServiceDescriptors(t *testing.T) {
	t.Run("Returns only services seen within threshold", func(t *testing.T) {
		now := time.Now()
		recent := now.Add(-5 * time.Second)
		stale := now.Add(-30 * time.Second)

		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{Name: "fresh-service", LastSeenAt: &recent},
				{Name: "stale-service", LastSeenAt: &stale},
			},
		}

		fresh := gsi.FreshServiceDescriptors(10 * time.Second)
		assert.Len(t, fresh, 1)
		assert.Equal(t, "fresh-service", fresh[0].Name)
	})

	t.Run("Returns empty when all services are stale", func(t *testing.T) {
		stale := time.Now().Add(-1 * time.Minute)

		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{Name: "service-a", LastSeenAt: &stale},
				{Name: "service-b", LastSeenAt: &stale},
			},
		}

		fresh := gsi.FreshServiceDescriptors(10 * time.Second)
		assert.Empty(t, fresh)
	})

	t.Run("Returns all when all services are fresh", func(t *testing.T) {
		recent := time.Now()

		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{Name: "service-a", LastSeenAt: &recent},
				{Name: "service-b", LastSeenAt: &recent},
			},
		}

		fresh := gsi.FreshServiceDescriptors(10 * time.Second)
		assert.Len(t, fresh, 2)
	})

	t.Run("Returns nil when closed", func(t *testing.T) {
		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{},
		}
		gsi.Close()

		fresh := gsi.FreshServiceDescriptors(10 * time.Second)
		assert.Nil(t, fresh)
	})
}

func TestGatewayServiceIndexer_PruneStaleServices(t *testing.T) {
	t.Run("Removes services past the threshold", func(t *testing.T) {
		recent := time.Now()
		stale := time.Now().Add(-1 * time.Minute)

		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{Name: "fresh-service", LastSeenAt: &recent},
				{Name: "stale-service", LastSeenAt: &stale},
			},
		}

		gsi.PruneStaleServices(30 * time.Second)
		assert.Len(t, gsi.ServiceDescriptors, 1)
		assert.Equal(t, "fresh-service", gsi.ServiceDescriptors[0].Name)
	})

	t.Run("Keeps all services when none are past threshold", func(t *testing.T) {
		recent := time.Now()

		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{Name: "service-a", LastSeenAt: &recent},
				{Name: "service-b", LastSeenAt: &recent},
			},
		}

		gsi.PruneStaleServices(30 * time.Second)
		assert.Len(t, gsi.ServiceDescriptors, 2)
	})

	t.Run("Removes all services when all are past threshold", func(t *testing.T) {
		stale := time.Now().Add(-1 * time.Minute)

		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{Name: "service-a", LastSeenAt: &stale},
				{Name: "service-b", LastSeenAt: &stale},
			},
		}

		gsi.PruneStaleServices(30 * time.Second)
		assert.Empty(t, gsi.ServiceDescriptors)
	})
}

func TestGatewayServiceIndexer_ResolveService(t *testing.T) {
	t.Run("Resolves matching route", func(t *testing.T) {
		now := time.Now()
		rd := mustRouteDescriptor("GET", "/test")

		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{
					Name:             "test-service",
					RouteDescriptors: []*zephyr.RouteDescriptor{rd},
					LastSeenAt:       &now,
				},
			},
		}

		sd, resolvedRd, ok := gsi.ResolveService("GET", "/test")
		assert.True(t, ok)
		assert.Equal(t, "test-service", sd.Name)
		assert.Equal(t, "GET", resolvedRd.Method)
	})

	t.Run("Returns false for non-matching method", func(t *testing.T) {
		now := time.Now()
		rd := mustRouteDescriptor("GET", "/test")

		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{
					Name:             "test-service",
					RouteDescriptors: []*zephyr.RouteDescriptor{rd},
					LastSeenAt:       &now,
				},
			},
		}

		_, _, ok := gsi.ResolveService("POST", "/test")
		assert.False(t, ok)
	})

	t.Run("Routes to stale services that have not been pruned", func(t *testing.T) {
		stale := time.Now().Add(-1 * time.Minute)
		rd := mustRouteDescriptor("GET", "/test")

		gsi := &zephyr.GatewayServiceIndexer{
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{
					Name:             "stale-service",
					RouteDescriptors: []*zephyr.RouteDescriptor{rd},
					LastSeenAt:       &stale,
				},
			},
		}

		sd, _, ok := gsi.ResolveService("GET", "/test")
		assert.True(t, ok)
		assert.Equal(t, "stale-service", sd.Name)
	})
}

func TestGateway_AnnouncePromptsStaleServiceToReAnnounce(t *testing.T) {
	t.Run("Service re-announces when excluded from gateway announcement", func(t *testing.T) {
		transport := localtransport.New()

		gateway := zephyr.NewGateway("test-gateway", transport)
		err := gateway.Start()
		assert.NoError(t, err)
		defer gateway.Stop()

		// Start a service — it announces and the gateway indexes it
		service := zephyr.NewService("test-service", transport, simpleHandler)
		err = service.Start()
		assert.NoError(t, err)
		defer service.Stop()

		// Track re-announcements from the service
		reannounced := false
		err = transport.BindServiceAnnounce(func(d *zephyr.ServiceDescriptor) {
			if d.Name == "test-service" {
				reannounced = true
			}
		})
		assert.NoError(t, err)

		// Simulate gateway announcing WITHOUT the service in the list.
		// The service should re-announce itself.
		err = transport.AnnounceGateway(&zephyr.GatewayDescriptor{
			Name:               "test-gateway",
			ServiceDescriptors: []*zephyr.ServiceDescriptor{},
		})
		assert.NoError(t, err)

		assert.True(t, reannounced, "Service should re-announce when not found in gateway announcement")
	})

	t.Run("Service does not re-announce when included in gateway announcement", func(t *testing.T) {
		transport := localtransport.New()

		service := zephyr.NewService("test-service", transport, simpleHandler)
		err := service.Start()
		assert.NoError(t, err)
		defer service.Stop()

		reannounced := false
		err = transport.BindServiceAnnounce(func(d *zephyr.ServiceDescriptor) {
			if d.Name == "test-service" {
				reannounced = true
			}
		})
		assert.NoError(t, err)

		// Announce WITH the service in the list
		err = transport.AnnounceGateway(&zephyr.GatewayDescriptor{
			Name: "test-gateway",
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{Name: "test-service"},
			},
		})
		assert.NoError(t, err)

		assert.False(t, reannounced, "Service should not re-announce when found in gateway announcement")
	})
}
