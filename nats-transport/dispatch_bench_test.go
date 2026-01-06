package natstransport_test

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/RobertWHurst/navaros"
	"github.com/RobertWHurst/zephyr"
	natstransport "github.com/RobertWHurst/zephyr/nats-transport"
	"github.com/nats-io/nats.go"
)

// getNATSURL returns the NATS server URL for testing
func getNATSURL() string {
	if url := os.Getenv("NATS_URL"); url != "" {
		return url
	}
	return "nats://localhost:4222"
}

// setupNATSConnection creates a NATS connection for testing
func setupNATSConnection(tb testing.TB) *nats.Conn {
	tb.Helper()

	url := getNATSURL()
	opts := []nats.Option{
		nats.Name(fmt.Sprintf("zephyr-bench-%s", tb.Name())),
		nats.MaxReconnects(3),
		nats.ReconnectWait(time.Second),
		nats.Timeout(5 * time.Second),
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		tb.Skipf("NATS connection failed (URL: %s): %v", url, err)
	}

	return nc
}

// simpleHandler is a basic handler for benchmarking
var simpleHandler = func(ctx *navaros.Context) {
	ctx.Status = 200
	ctx.Body = "OK"
}

// echoHandler echoes the request body
var echoHandler = func(ctx *navaros.Context) {
	body := make([]byte, 0)
	buf := make([]byte, 1024)
	for {
		n, err := ctx.Request().Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	ctx.Status = 200
	ctx.Body = body
}

func mustRouteDescriptor(method, pattern string) *zephyr.RouteDescriptor {
	rd, err := zephyr.NewRouteDescriptor(method, pattern)
	if err != nil {
		panic(err)
	}
	return rd
}

// setupBenchService creates and starts a test service
func setupBenchService(transport *natstransport.NatsTransport, name string) *zephyr.Service {
	router := navaros.NewRouter()
	router.Get("/test", simpleHandler)
	router.Post("/test", echoHandler)

	service := zephyr.NewService(name, transport, router)
	service.RouteDescriptors = []*zephyr.RouteDescriptor{
		mustRouteDescriptor("GET", "/test"),
		mustRouteDescriptor("POST", "/test"),
	}

	if err := service.Start(); err != nil {
		panic(fmt.Sprintf("Failed to start test service: %v", err))
	}

	return service
}

// BenchmarkNatsDispatch_HighVolume tests memory stability under high request volume
func BenchmarkNatsDispatch_HighVolume(b *testing.B) {
	nc := setupNATSConnection(b)
	defer nc.Close()

	transport := natstransport.New(nc)

	gateway := zephyr.NewGateway("bench-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	service := setupBenchService(transport, "high-volume-service")
	defer service.Stop()

	// Allow service discovery
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		transport.Dispatch("high-volume-service", rec, req)
	}
}

// BenchmarkNatsDispatch_Concurrent tests concurrent request handling
func BenchmarkNatsDispatch_Concurrent(b *testing.B) {
	nc := setupNATSConnection(b)
	defer nc.Close()

	transport := natstransport.New(nc)

	gateway := zephyr.NewGateway("bench-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	service := setupBenchService(transport, "concurrent-service")
	defer service.Stop()

	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetParallelism(50)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			transport.Dispatch("concurrent-service", rec, req)
		}
	})
}

// BenchmarkNatsDispatch_LargePayload tests memory handling with large payloads
func BenchmarkNatsDispatch_LargePayload(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
	}

	nc := setupNATSConnection(b)
	defer nc.Close()

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			transport := natstransport.New(nc)

			gateway := zephyr.NewGateway("bench-gateway", transport)
			gateway.Start()
			defer gateway.Stop()

			service := setupBenchService(transport, fmt.Sprintf("payload-service-%s", size.name))
			defer service.Stop()

			time.Sleep(100 * time.Millisecond)

			payload := bytes.Repeat([]byte("x"), size.size)

			b.ResetTimer()
			b.ReportAllocs()
			b.SetBytes(int64(size.size * 2)) // Request + Response

			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest("POST", "/test", bytes.NewReader(payload))
				rec := httptest.NewRecorder()
				transport.Dispatch(fmt.Sprintf("payload-service-%s", size.name), rec, req)
			}
		})
	}
}

// TestNatsSubscriptionLeak verifies subscriptions are properly cleaned up
func TestNatsSubscriptionLeak(t *testing.T) {
	nc := setupNATSConnection(t)
	defer nc.Close()

	transport := natstransport.New(nc)

	gateway := zephyr.NewGateway("sub-leak-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	// Record initial subscription count
	initialSubs := nc.NumSubscriptions()
	t.Logf("Initial subscriptions: %d", initialSubs)

	const numIterations = 100

	for i := 0; i < numIterations; i++ {
		serviceName := fmt.Sprintf("sub-test-service-%d", i)

		service := zephyr.NewService(serviceName, transport, simpleHandler)
		service.Start()

		time.Sleep(20 * time.Millisecond)

		// Execute some requests
		for j := 0; j < 5; j++ {
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			transport.Dispatch(serviceName, rec, req)
		}

		service.Stop()
	}

	// Give time for cleanup
	time.Sleep(200 * time.Millisecond)

	finalSubs := nc.NumSubscriptions()
	t.Logf("Final subscriptions: %d", finalSubs)

	// Subscriptions should return close to initial count
	maxAllowedGrowth := 20 // Allow margin for internal NATS subscriptions
	if finalSubs-initialSubs > maxAllowedGrowth {
		t.Errorf("Subscription leak detected: started with %d, ended with %d (growth: %d)",
			initialSubs, finalSubs, finalSubs-initialSubs)
	}
}

// TestNatsDispatchMemoryProfile runs dispatch with detailed memory profiling
func TestNatsDispatchMemoryProfile(t *testing.T) {
	if os.Getenv("ZEPHYR_PROFILE") != "1" {
		t.Skip("Profiling disabled. Set ZEPHYR_PROFILE=1 to enable")
	}

	nc := setupNATSConnection(t)
	defer nc.Close()

	transport := natstransport.New(nc)

	gateway := zephyr.NewGateway("profile-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	service := setupBenchService(transport, "profile-service")
	defer service.Stop()

	time.Sleep(100 * time.Millisecond)

	const iterations = 1000
	const sampleInterval = 100

	var memSamples []uint64

	// Warmup
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		transport.Dispatch("profile-service", rec, req)
	}

	runtime.GC()

	for i := 0; i < iterations; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		transport.Dispatch("profile-service", rec, req)

		if i > 0 && i%sampleInterval == 0 {
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			memSamples = append(memSamples, m.HeapAlloc)
			t.Logf("Iteration %d: HeapAlloc=%d, NumGC=%d", i, m.HeapAlloc, m.NumGC)
		}
	}

	// Check for consistent growth
	if len(memSamples) >= 3 {
		growthCount := 0
		for i := 1; i < len(memSamples); i++ {
			if memSamples[i] > memSamples[i-1] {
				growthCount++
			}
		}
		growthRatio := float64(growthCount) / float64(len(memSamples)-1)
		t.Logf("Growth ratio: %.2f%% of samples showed increase", growthRatio*100)

		if growthRatio > 0.8 {
			t.Errorf("Memory appears to be consistently growing")
		}
	}
}

// BenchmarkNatsDispatch_ServiceStartStop benchmarks service lifecycle
func BenchmarkNatsDispatch_ServiceStartStop(b *testing.B) {
	nc := setupNATSConnection(b)
	defer nc.Close()

	transport := natstransport.New(nc)

	gateway := zephyr.NewGateway("bench-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		service := zephyr.NewService(
			fmt.Sprintf("lifecycle-service-%d", i%100),
			transport,
			simpleHandler,
		)
		service.Start()
		service.Stop()
	}
}
