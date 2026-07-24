package natstransport_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/RobertWHurst/zephyr/v2"
	natstransport "github.com/RobertWHurst/zephyr/v2/nats-transport"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startNats runs an embedded NATS server and returns a client connection.
func startNats(t *testing.T) *nats.Conn {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{Port: -1})
	require.NoError(t, err)
	go server.Start()
	if !server.ReadyForConnections(5 * time.Second) {
		t.Fatal("embedded NATS server did not start")
	}
	t.Cleanup(server.Shutdown)

	conn, err := nats.Connect(server.ClientURL())
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	return conn
}

// serveSub serves a subscription until the returned stop function is called.
func serveSub(t *testing.T, subscribeFn func(ctx context.Context) (zephyr.Subscription, error)) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := subscribeFn(ctx)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- sub.Serve(ctx) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("subscription did not stop")
		}
	}
}

func TestNatsTransport_Dispatch_RoundTrip(t *testing.T) {
	transport := natstransport.New(startNats(t))

	stop := serveSub(t, func(ctx context.Context) (zephyr.Subscription, error) {
		return transport.SubscribeDispatch(ctx, "svc-1", func(res http.ResponseWriter, req *http.Request) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			res.Header().Set("X-Received-Method", req.Method)
			res.Header().Set("X-Received-Header", req.Header.Get("X-Test"))
			res.WriteHeader(201)
			_, _ = res.Write([]byte("echo:"))
			_, _ = res.Write(body)
		})
	})
	defer stop()

	req := httptest.NewRequest("POST", "http://gateway.local/items", bytes.NewReader([]byte("hello body")))
	req.Header.Set("X-Test", "header-value")
	rec := httptest.NewRecorder()

	require.NoError(t, transport.Dispatch("svc-1", rec, req))

	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	assert.Equal(t, 201, res.StatusCode)
	assert.Equal(t, "POST", res.Header.Get("X-Received-Method"))
	assert.Equal(t, "header-value", res.Header.Get("X-Received-Header"))
	assert.Equal(t, "echo:hello body", string(body))
}

func TestNatsTransport_Dispatch_LargeBodiesRoundTrip(t *testing.T) {
	transport := natstransport.New(startNats(t))

	// Multiple chunks in both directions (chunk size is 16KB).
	requestBody := bytes.Repeat([]byte("q"), 200*1024)

	stop := serveSub(t, func(ctx context.Context) (zephyr.Subscription, error) {
		return transport.SubscribeDispatch(ctx, "svc-1", func(res http.ResponseWriter, req *http.Request) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			res.WriteHeader(200)
			_, _ = res.Write(body)
		})
	})
	defer stop()

	req := httptest.NewRequest("POST", "http://gateway.local/echo", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()

	require.NoError(t, transport.Dispatch("svc-1", rec, req))

	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	assert.Equal(t, 200, res.StatusCode)
	require.Equal(t, len(requestBody), len(body))
	assert.True(t, bytes.Equal(requestBody, body), "large body should survive chunked transfer both ways")
}

func TestNatsTransport_Dispatch_ConcurrentRequests(t *testing.T) {
	transport := natstransport.New(startNats(t))

	stop := serveSub(t, func(ctx context.Context) (zephyr.Subscription, error) {
		return transport.SubscribeDispatch(ctx, "svc-1", func(res http.ResponseWriter, req *http.Request) {
			body, _ := io.ReadAll(req.Body)
			res.WriteHeader(200)
			_, _ = res.Write(body)
		})
	})
	defer stop()

	const concurrency = 16
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)
	for i := range concurrency {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte('a' + n%26)}, 4096)
			req := httptest.NewRequest("POST", "http://gateway.local/echo", bytes.NewReader(payload))
			rec := httptest.NewRecorder()
			if err := transport.Dispatch("svc-1", rec, req); err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(rec.Body.Bytes(), payload) {
				errs <- assert.AnError
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent dispatch failed: %v", err)
	}
}

func TestNatsTransport_QueueGroup_BalancesAcrossInstances(t *testing.T) {
	transport := natstransport.New(startNats(t))

	var mu sync.Mutex
	counts := map[string]int{}
	makeHandler := func(name string) func(res http.ResponseWriter, req *http.Request) {
		return func(res http.ResponseWriter, req *http.Request) {
			mu.Lock()
			counts[name]++
			mu.Unlock()
			res.WriteHeader(200)
		}
	}

	stop1 := serveSub(t, func(ctx context.Context) (zephyr.Subscription, error) {
		return transport.SubscribeDispatch(ctx, "svc-1", makeHandler("instance-1"))
	})
	defer stop1()
	stop2 := serveSub(t, func(ctx context.Context) (zephyr.Subscription, error) {
		return transport.SubscribeDispatch(ctx, "svc-1", makeHandler("instance-2"))
	})
	defer stop2()

	total := 0
	for range 40 {
		req := httptest.NewRequest("GET", "http://gateway.local/x", nil)
		rec := httptest.NewRecorder()
		require.NoError(t, transport.Dispatch("svc-1", rec, req))
		total++
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, total, counts["instance-1"]+counts["instance-2"],
		"every request must be handled exactly once across the queue group")
}

func TestNatsTransport_Announcements_RoundTrip(t *testing.T) {
	transport := natstransport.New(startNats(t))

	gatewayAnnounces := make(chan *zephyr.GatewayDescriptor, 1)
	stopGateway := serveSub(t, func(ctx context.Context) (zephyr.Subscription, error) {
		return transport.SubscribeGatewayAnnouncements(ctx, func(descriptor *zephyr.GatewayDescriptor) {
			gatewayAnnounces <- descriptor
		})
	})
	defer stopGateway()

	serviceAnnounces := make(chan *zephyr.ServiceDescriptor, 1)
	stopService := serveSub(t, func(ctx context.Context) (zephyr.Subscription, error) {
		return transport.SubscribeServiceAnnouncements(ctx, func(descriptor *zephyr.ServiceDescriptor) {
			serviceAnnounces <- descriptor
		})
	})
	defer stopService()

	route, err := zephyr.NewRouteDescriptor("GET", "/api/items/:id")
	require.NoError(t, err)
	require.NoError(t, transport.AnnounceGateway(&zephyr.GatewayDescriptor{Name: "gw-1"}))
	require.NoError(t, transport.AnnounceService(&zephyr.ServiceDescriptor{
		Name:             "svc-1",
		RouteDescriptors: []*zephyr.RouteDescriptor{route},
	}))

	select {
	case descriptor := <-gatewayAnnounces:
		assert.Equal(t, "gw-1", descriptor.Name)
	case <-time.After(5 * time.Second):
		t.Fatal("gateway announcement not delivered")
	}
	select {
	case descriptor := <-serviceAnnounces:
		assert.Equal(t, "svc-1", descriptor.Name)
		require.Len(t, descriptor.RouteDescriptors, 1)
		require.NotNil(t, descriptor.RouteDescriptors[0].Pattern)
		_, matched := descriptor.RouteDescriptors[0].Pattern.Match("/api/items/42")
		assert.True(t, matched, "route pattern must survive the msgpack round trip")
	case <-time.After(5 * time.Second):
		t.Fatal("service announcement not delivered")
	}
}

func TestNatsTransport_Dispatch_NilBodyAndTLSState(t *testing.T) {
	transport := natstransport.New(startNats(t))

	sawTLS := make(chan bool, 1)
	stop := serveSub(t, func(ctx context.Context) (zephyr.Subscription, error) {
		return transport.SubscribeDispatch(ctx, "svc-1", func(res http.ResponseWriter, req *http.Request) {
			sawTLS <- req.TLS != nil && req.TLS.ServerName == "gateway.local"
			res.WriteHeader(204)
		})
	})
	defer stop()

	// A raw request with a nil body and TLS connection state, as a real
	// terminated-TLS gateway request would carry.
	req := httptest.NewRequest("GET", "https://gateway.local/x", nil)
	req.Body = nil
	req.TLS = &tls.ConnectionState{
		HandshakeComplete: true,
		ServerName:        "gateway.local",
	}
	rec := httptest.NewRecorder()

	require.NoError(t, transport.Dispatch("svc-1", rec, req))
	assert.Equal(t, 204, rec.Result().StatusCode)
	assert.True(t, <-sawTLS, "TLS connection state should be forwarded to the service")
}

func TestNatsTransport_Dispatch_HandlerPanicStillResponds(t *testing.T) {
	transport := natstransport.New(startNats(t))

	stop := serveSub(t, func(ctx context.Context) (zephyr.Subscription, error) {
		return transport.SubscribeDispatch(ctx, "svc-1", func(res http.ResponseWriter, req *http.Request) {
			res.WriteHeader(200)
			panic("handler exploded")
		})
	})
	defer stop()

	req := httptest.NewRequest("GET", "http://gateway.local/x", nil)
	rec := httptest.NewRecorder()

	// The dispatch must complete (no hang, no dropped response) even though
	// the handler panicked after writing headers.
	require.NoError(t, transport.Dispatch("svc-1", rec, req))
	assert.Equal(t, 200, rec.Result().StatusCode)
}
