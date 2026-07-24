package zephyr_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/RobertWHurst/navaros"
	"github.com/RobertWHurst/zephyr"
	localtransport "github.com/RobertWHurst/zephyr/local-transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEchoService starts a service with GET /test and POST /echo routes.
func startEchoService(t *testing.T, transport *localtransport.LocalTransport, name string) *zephyr.Service {
	t.Helper()

	router := navaros.NewRouter()
	router.Get("/test", func(ctx *navaros.Context) {
		ctx.Status = 200
		ctx.Headers.Set("X-Service", name)
		ctx.Body = "OK"
	})
	router.Post("/echo", func(ctx *navaros.Context) {
		body, err := io.ReadAll(ctx.Request().Body)
		if err != nil {
			ctx.Status = 500
			return
		}
		ctx.Status = 200
		ctx.Body = body
	})

	service := zephyr.NewService(name, transport, router)
	getRoute, err := zephyr.NewRouteDescriptor("GET", "/test")
	require.NoError(t, err)
	postRoute, err := zephyr.NewRouteDescriptor("POST", "/echo")
	require.NoError(t, err)
	service.RouteDescriptors = []*zephyr.RouteDescriptor{getRoute, postRoute}

	require.NoError(t, startService(t, service))
	return service
}

func TestGateway_ServeHTTP_RoutesToService(t *testing.T) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("test-gateway", transport)
	require.NoError(t, startGateway(t, gateway))
	startEchoService(t, transport, "echo-service")

	server := httptest.NewServer(gateway)
	defer server.Close()

	res, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	assert.Equal(t, 200, res.StatusCode)
	assert.Equal(t, "OK", string(body))
	assert.Equal(t, "echo-service", res.Header.Get("X-Service"))
}

func TestGateway_ServeHTTP_UnknownRouteReturns404(t *testing.T) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("test-gateway", transport)
	require.NoError(t, startGateway(t, gateway))
	startEchoService(t, transport, "echo-service")

	server := httptest.NewServer(gateway)
	defer server.Close()

	res, err := http.Get(server.URL + "/no-such-route")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, 404, res.StatusCode)
}

func TestGateway_ServeHTTP_BeforeConnectReturns503(t *testing.T) {
	gateway := zephyr.NewGateway("test-gateway", localtransport.New())

	server := httptest.NewServer(gateway)
	defer server.Close()

	res, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, 503, res.StatusCode)
}

func TestGateway_CanServeHTTP(t *testing.T) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("test-gateway", transport)

	req := httptest.NewRequest("GET", "/test", nil)
	assert.False(t, gateway.CanServeHTTP(req), "not connected yet")

	require.NoError(t, startGateway(t, gateway))
	startEchoService(t, transport, "echo-service")

	assert.True(t, gateway.CanServeHTTP(req))
	assert.False(t, gateway.CanServeHTTP(httptest.NewRequest("GET", "/nope", nil)))
}

func TestGateway_NavarosMounting_HandlesAndFallsThrough(t *testing.T) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("test-gateway", transport)
	require.NoError(t, startGateway(t, gateway))
	startEchoService(t, transport, "echo-service")

	router := navaros.NewRouter()
	router.Use(gateway)
	router.Get("/local", func(ctx *navaros.Context) {
		ctx.Status = 200
		ctx.Body = "local"
	})

	server := httptest.NewServer(router)
	defer server.Close()

	// Routed through the gateway to the service.
	res, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
	assert.Equal(t, "OK", string(body))

	// Not a service route — falls through to the router's own handler.
	res, err = http.Get(server.URL + "/local")
	require.NoError(t, err)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
	assert.Equal(t, "local", string(body))
}

func TestGateway_DescriptorMiddleware_SetsContextValues(t *testing.T) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("test-gateway", transport)
	require.NoError(t, startGateway(t, gateway))
	startEchoService(t, transport, "echo-service")

	var routeDescriptor *zephyr.RouteDescriptor
	var serviceDescriptor *zephyr.ServiceDescriptor
	var serviceDescriptors []*zephyr.ServiceDescriptor

	router := navaros.NewRouter()
	router.Use(gateway.DescriptorMiddleware())
	router.Use(func(ctx *navaros.Context) {
		routeDescriptor = zephyr.RouteDescriptorFromContext(ctx)
		serviceDescriptor = zephyr.ServiceDescriptorFromContext(ctx)
		serviceDescriptors = zephyr.ServiceDescriptorsFromContext(ctx)
		ctx.Next()
	})
	router.Use(gateway.DispatchMiddleware())

	server := httptest.NewServer(router)
	defer server.Close()

	res, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)

	require.NotNil(t, routeDescriptor)
	assert.Equal(t, "GET", routeDescriptor.Method)
	require.NotNil(t, serviceDescriptor)
	assert.Equal(t, "echo-service", serviceDescriptor.Name)
	require.Len(t, serviceDescriptors, 1)
}

func TestClient_RequestsAgainstService(t *testing.T) {
	transport := localtransport.New()
	startEchoService(t, transport, "echo-service")

	client := zephyr.NewClient(transport).Service("echo-service")

	res, err := client.Get("/test")
	require.NoError(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
	assert.Equal(t, "OK", string(body))

	res, err = client.Post("/echo", "text/plain", strings.NewReader("ping"))
	require.NoError(t, err)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
	assert.Equal(t, "ping", string(body))

	res, err = client.PostForm("/echo", url.Values{"a": {"1"}})
	require.NoError(t, err)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, "a=1", string(body))

	// The service registers no HEAD route, so the request round-trips but 404s.
	res, err = client.Head("/test")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, 404, res.StatusCode)
}

func TestClient_ServeHTTPProxiesToService(t *testing.T) {
	transport := localtransport.New()
	startEchoService(t, transport, "echo-service")

	proxy := zephyr.NewClient(transport).Service("echo-service")
	server := httptest.NewServer(proxy)
	defer server.Close()

	res, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
	assert.Equal(t, "OK", string(body))
}

func TestClient_NavarosHandleProxiesToService(t *testing.T) {
	transport := localtransport.New()
	startEchoService(t, transport, "echo-service")

	proxy := zephyr.NewClient(transport).Service("echo-service")

	router := navaros.NewRouter()
	router.Use(proxy.Handle)

	server := httptest.NewServer(router)
	defer server.Close()

	res, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
	assert.Equal(t, "OK", string(body))
}
