package zephyr_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RobertWHurst/navaros"
	"github.com/RobertWHurst/zephyr/v2"
	localtransport "github.com/RobertWHurst/zephyr/v2/local-transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

type routePolicy struct {
	Auth      string `msgpack:"auth" json:"auth"`
	RateLimit int    `msgpack:"rateLimit" json:"rateLimit"`
}

func TestRouteDescriptor_MetadataSurvivesMsgpackAsTypedValue(t *testing.T) {
	route, err := zephyr.NewRouteDescriptor("GET", "/api/items/:id")
	require.NoError(t, err)
	route.Metadata = routePolicy{Auth: "required", RateLimit: 100}

	data, err := msgpack.Marshal(route)
	require.NoError(t, err)

	decoded := &zephyr.RouteDescriptor{}
	require.NoError(t, msgpack.Unmarshal(data, decoded))

	var policy routePolicy
	require.NoError(t, decoded.UnmarshalMetadata(&policy))
	assert.Equal(t, "required", policy.Auth)
	assert.Equal(t, 100, policy.RateLimit)
}

func TestRouteDescriptor_UnmarshalMetadataOnLocalDescriptor(t *testing.T) {
	// Descriptors that never cross the wire (local transport) hold the
	// original value; UnmarshalMetadata must behave identically.
	route, err := zephyr.NewRouteDescriptor("GET", "/api/items/:id")
	require.NoError(t, err)
	route.Metadata = routePolicy{Auth: "required", RateLimit: 5}

	var policy routePolicy
	require.NoError(t, route.UnmarshalMetadata(&policy))
	assert.Equal(t, "required", policy.Auth)
	assert.Equal(t, 5, policy.RateLimit)
}

func TestRouteDescriptor_UnmarshalMetadataWithoutMetadata(t *testing.T) {
	route, err := zephyr.NewRouteDescriptor("GET", "/api/items")
	require.NoError(t, err)

	var policy routePolicy
	err = route.UnmarshalMetadata(&policy)
	assert.ErrorIs(t, err, zephyr.ErrNoRouteMetadata)
}

// TestGateway_RouteMetadataAvailableToEdgeMiddleware exercises the whole
// mechanism: navaros WithMetadata at the bind site → service announcement →
// gateway index → DescriptorMiddleware → typed decode at the edge.
func TestGateway_RouteMetadataAvailableToEdgeMiddleware(t *testing.T) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("test-gateway", transport)
	require.NoError(t, startGateway(t, gateway))

	serviceRouter := navaros.NewRouter()
	serviceRouter.PublicGet("/secure", navaros.WithMetadata(routePolicy{
		Auth:      "required",
		RateLimit: 42,
	}), func(ctx *navaros.Context) {
		ctx.Status = 200
		ctx.Body = "OK"
	})

	service := zephyr.NewService("secure-service", transport, serviceRouter)
	require.NoError(t, startService(t, service))

	var policy routePolicy
	var decodeErr error

	edgeRouter := navaros.NewRouter()
	edgeRouter.Use(gateway.DescriptorMiddleware())
	edgeRouter.Use(func(ctx *navaros.Context) {
		if rd := zephyr.RouteDescriptorFromContext(ctx); rd != nil {
			decodeErr = rd.UnmarshalMetadata(&policy)
		}
		ctx.Next()
	})
	edgeRouter.Use(gateway.DispatchMiddleware())

	server := httptest.NewServer(edgeRouter)
	defer server.Close()

	res, err := http.Get(server.URL + "/secure")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, 200, res.StatusCode)

	require.NoError(t, decodeErr)
	assert.Equal(t, "required", policy.Auth)
	assert.Equal(t, 42, policy.RateLimit)
}
