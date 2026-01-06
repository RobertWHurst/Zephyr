package localtransport_test

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"

	"github.com/RobertWHurst/navaros"
	"github.com/RobertWHurst/zephyr"
	localtransport "github.com/RobertWHurst/zephyr/local-transport"
)

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
func setupBenchService(transport *localtransport.LocalTransport, name string) *zephyr.Service {
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

// BenchmarkLocalDispatch_HighVolume tests memory stability under high request volume
func BenchmarkLocalDispatch_HighVolume(b *testing.B) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("bench-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	service := setupBenchService(transport, "high-volume-service")
	defer service.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		transport.Dispatch("high-volume-service", rec, req)
	}
}

// BenchmarkLocalDispatch_Concurrent tests concurrent request handling
func BenchmarkLocalDispatch_Concurrent(b *testing.B) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("bench-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	service := setupBenchService(transport, "concurrent-service")
	defer service.Stop()

	b.ResetTimer()
	b.ReportAllocs()
	b.SetParallelism(100)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			transport.Dispatch("concurrent-service", rec, req)
		}
	})
}

// BenchmarkLocalDispatch_LargePayload tests memory handling with large payloads
func BenchmarkLocalDispatch_LargePayload(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
		{"1MB", 1024 * 1024},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			transport := localtransport.New()

			gateway := zephyr.NewGateway("bench-gateway", transport)
			gateway.Start()
			defer gateway.Stop()

			service := setupBenchService(transport, fmt.Sprintf("payload-service-%s", size.name))
			defer service.Stop()

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

// BenchmarkLocalDispatch_ServiceStartStop benchmarks service lifecycle
func BenchmarkLocalDispatch_ServiceStartStop(b *testing.B) {
	transport := localtransport.New()

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

// TestLocalDispatchMemoryProfile runs dispatch with detailed memory profiling
func TestLocalDispatchMemoryProfile(t *testing.T) {
	if os.Getenv("ZEPHYR_PROFILE") != "1" {
		t.Skip("Profiling disabled. Set ZEPHYR_PROFILE=1 to enable")
	}

	transport := localtransport.New()

	gateway := zephyr.NewGateway("profile-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	service := setupBenchService(transport, "profile-service")
	defer service.Stop()

	const iterations = 5000
	const sampleInterval = 500

	var memSamples []uint64

	// Warmup
	for i := 0; i < 500; i++ {
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

// TestLocalServiceRegistrationMemory tests service registration/deregistration memory
func TestLocalServiceRegistrationMemory(t *testing.T) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("test-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	initialGoroutines := runtime.NumGoroutine()
	t.Logf("Initial goroutines: %d", initialGoroutines)

	const cycles = 100

	// Warmup
	for i := 0; i < 10; i++ {
		service := zephyr.NewService(fmt.Sprintf("warmup-%d", i), transport, simpleHandler)
		service.Start()
		service.Stop()
	}

	runtime.GC()
	var baselineMem runtime.MemStats
	runtime.ReadMemStats(&baselineMem)

	for i := 0; i < cycles; i++ {
		service := zephyr.NewService(fmt.Sprintf("cycle-service-%d", i), transport, simpleHandler)
		service.Start()

		// Do some work
		for j := 0; j < 50; j++ {
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			transport.Dispatch(fmt.Sprintf("cycle-service-%d", i), rec, req)
		}

		service.Stop()
	}

	runtime.GC()
	runtime.GC()

	var finalMem runtime.MemStats
	runtime.ReadMemStats(&finalMem)

	finalGoroutines := runtime.NumGoroutine()

	t.Logf("Memory: baseline=%d, final=%d, diff=%d",
		baselineMem.HeapAlloc, finalMem.HeapAlloc, finalMem.HeapAlloc-baselineMem.HeapAlloc)
	t.Logf("Goroutines: initial=%d, final=%d", initialGoroutines, finalGoroutines)

	// Check for significant memory growth
	memGrowth := finalMem.HeapAlloc - baselineMem.HeapAlloc
	maxGrowthBytes := uint64(5 * 1024 * 1024) // 5MB max growth
	if memGrowth > maxGrowthBytes {
		t.Errorf("Significant memory growth detected: %d bytes (max: %d)", memGrowth, maxGrowthBytes)
	}

	// Check for goroutine leaks
	goroutineLeak := finalGoroutines - initialGoroutines
	if goroutineLeak > 5 {
		t.Errorf("Goroutine leak detected: %d goroutines leaked", goroutineLeak)
	}
}
