package zephyr_test

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/RobertWHurst/zephyr"
	localtransport "github.com/RobertWHurst/zephyr/local-transport"
	natstransport "github.com/RobertWHurst/zephyr/nats-transport"
)

// TestIterativeMemoryGrowth_Local runs multiple iterations and checks for memory growth using local transport
func TestIterativeMemoryGrowth_Local(t *testing.T) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("test-gateway", transport)
	if err := gateway.Start(); err != nil {
		t.Fatalf("Failed to start gateway: %v", err)
	}
	defer gateway.Stop()

	service := setupTestService(transport, "growth-test-service")
	defer service.Stop()

	const (
		warmupIterations = 500
		testIterations   = 3000
		sampleInterval   = 500
	)

	// Warmup phase
	for i := 0; i < warmupIterations; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		transport.Dispatch("growth-test-service", rec, req)
	}

	// Force GC after warmup
	runtime.GC()
	runtime.GC()

	profiler := NewMemoryProfiler(t, "iterative_growth_local")
	profiler.Snapshot("baseline")

	// Test phase
	for i := 0; i < testIterations; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		transport.Dispatch("growth-test-service", rec, req)

		if i > 0 && i%sampleInterval == 0 {
			profiler.Snapshot(fmt.Sprintf("iteration_%d", i))
		}
	}

	profiler.Snapshot("final")

	// Analyze growth trend
	slope := profiler.GetMemoryGrowthSlope()
	bytesPerIteration := slope / float64(sampleInterval)

	t.Logf("Memory trend: %.2f bytes per iteration", bytesPerIteration)

	// Flag if growing more than 100 bytes per request (allowing some tolerance)
	if bytesPerIteration > 100 {
		t.Errorf("Memory growing at %.2f bytes per iteration (threshold: 100)", bytesPerIteration)
		profiler.WriteHeapProfile("growth_detected")
	}

	profiler.AssertNoGrowth(50.0) // Max 50% growth allowed
}

// TestIterativeMemoryGrowth_NATS runs multiple iterations and checks for memory growth using NATS transport
func TestIterativeMemoryGrowth_NATS(t *testing.T) {
	nc := setupNATSConnection(t)
	defer nc.Close()

	transport := natstransport.New(nc)

	gateway := zephyr.NewGateway("test-gateway", transport)
	if err := gateway.Start(); err != nil {
		t.Fatalf("Failed to start gateway: %v", err)
	}
	defer gateway.Stop()

	service := setupTestService(transport, "growth-test-service-nats")
	defer service.Stop()

	// Allow service discovery
	time.Sleep(100 * time.Millisecond)

	const (
		warmupIterations = 200
		testIterations   = 1000
		sampleInterval   = 200
	)

	// Warmup phase
	for i := 0; i < warmupIterations; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		transport.Dispatch("growth-test-service-nats", rec, req)
	}

	// Force GC after warmup
	runtime.GC()
	runtime.GC()

	profiler := NewMemoryProfiler(t, "iterative_growth_nats")
	profiler.Snapshot("baseline")

	// Test phase
	for i := 0; i < testIterations; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		transport.Dispatch("growth-test-service-nats", rec, req)

		if i > 0 && i%sampleInterval == 0 {
			profiler.Snapshot(fmt.Sprintf("iteration_%d", i))
		}
	}

	profiler.Snapshot("final")

	slope := profiler.GetMemoryGrowthSlope()
	bytesPerIteration := slope / float64(sampleInterval)

	t.Logf("Memory trend: %.2f bytes per iteration", bytesPerIteration)

	if bytesPerIteration > 200 {
		t.Errorf("Memory growing at %.2f bytes per iteration (threshold: 200)", bytesPerIteration)
		profiler.WriteHeapProfile("growth_detected")
	}

	profiler.AssertNoGrowth(50.0)
}

// TestGoroutineLeak_Local tests for goroutine leaks with local transport
func TestGoroutineLeak_Local(t *testing.T) {
	transport := localtransport.New()

	initialGoroutines := runtime.NumGoroutine()
	t.Logf("Initial goroutines: %d", initialGoroutines)

	const cycles = 30

	for cycle := 0; cycle < cycles; cycle++ {
		service := zephyr.NewService(
			fmt.Sprintf("goroutine-test-local-%d", cycle),
			transport,
			simpleHandler,
		)
		service.Start()

		// Execute requests
		for i := 0; i < 50; i++ {
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			transport.Dispatch(fmt.Sprintf("goroutine-test-local-%d", cycle), rec, req)
		}

		service.Stop()
	}

	// Allow time for goroutines to exit
	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Final goroutines: %d", finalGoroutines)

	maxGrowth := 10
	if finalGoroutines-initialGoroutines > maxGrowth {
		t.Errorf("Goroutine leak: started with %d, ended with %d (leaked %d)",
			initialGoroutines, finalGoroutines, finalGoroutines-initialGoroutines)

		// Dump goroutine stacks for debugging
		buf := make([]byte, 64*1024)
		n := runtime.Stack(buf, true)
		t.Logf("Goroutine dump:\n%s", buf[:n])
	}
}

// TestGoroutineLeak_NATS tests for goroutine leaks with NATS transport
func TestGoroutineLeak_NATS(t *testing.T) {
	nc := setupNATSConnection(t)
	defer nc.Close()

	transport := natstransport.New(nc)

	initialGoroutines := runtime.NumGoroutine()
	t.Logf("Initial goroutines: %d", initialGoroutines)

	const cycles = 20

	for cycle := 0; cycle < cycles; cycle++ {
		service := zephyr.NewService(
			fmt.Sprintf("goroutine-test-nats-%d", cycle),
			transport,
			simpleHandler,
		)
		service.Start()

		// Execute requests
		for i := 0; i < 30; i++ {
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			transport.Dispatch(fmt.Sprintf("goroutine-test-nats-%d", cycle), rec, req)
		}

		service.Stop()
	}

	// Allow time for goroutines to exit
	time.Sleep(300 * time.Millisecond)
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Final goroutines: %d", finalGoroutines)

	maxGrowth := 10
	if finalGoroutines-initialGoroutines > maxGrowth {
		t.Errorf("Goroutine leak: started with %d, ended with %d (leaked %d)",
			initialGoroutines, finalGoroutines, finalGoroutines-initialGoroutines)

		buf := make([]byte, 64*1024)
		n := runtime.Stack(buf, true)
		t.Logf("Goroutine dump:\n%s", buf[:n])
	}
}

// TestServiceRegistrationCycle_Local tests service start/stop cycles for memory leaks
func TestServiceRegistrationCycle_Local(t *testing.T) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("test-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	profiler := NewMemoryProfiler(t, "service_registration_local")

	const cycles = 50

	// Warmup
	for i := 0; i < 10; i++ {
		service := zephyr.NewService(fmt.Sprintf("warmup-service-%d", i), transport, simpleHandler)
		service.Start()
		service.Stop()
	}

	runtime.GC()
	profiler.Snapshot("baseline")

	for i := 0; i < cycles; i++ {
		service := zephyr.NewService(fmt.Sprintf("cycle-service-%d", i), transport, simpleHandler)
		service.Start()

		// Do some work
		for j := 0; j < 20; j++ {
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			transport.Dispatch(fmt.Sprintf("cycle-service-%d", i), rec, req)
		}

		service.Stop()

		if i > 0 && i%10 == 0 {
			runtime.GC()
			profiler.Snapshot(fmt.Sprintf("cycle_%d", i))
		}
	}

	runtime.GC()
	profiler.Snapshot("final")

	profiler.AssertNoGrowth(30.0) // Max 30% growth
}

// TestServiceRegistrationCycle_NATS tests service start/stop cycles with NATS
func TestServiceRegistrationCycle_NATS(t *testing.T) {
	nc := setupNATSConnection(t)
	defer nc.Close()

	transport := natstransport.New(nc)

	gateway := zephyr.NewGateway("test-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	profiler := NewMemoryProfiler(t, "service_registration_nats")

	const cycles = 30

	// Warmup
	for i := 0; i < 5; i++ {
		service := zephyr.NewService(fmt.Sprintf("warmup-service-nats-%d", i), transport, simpleHandler)
		service.Start()
		time.Sleep(50 * time.Millisecond)
		service.Stop()
	}

	runtime.GC()
	profiler.Snapshot("baseline")

	for i := 0; i < cycles; i++ {
		service := zephyr.NewService(fmt.Sprintf("cycle-service-nats-%d", i), transport, simpleHandler)
		service.Start()
		time.Sleep(20 * time.Millisecond)

		// Do some work
		for j := 0; j < 10; j++ {
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			transport.Dispatch(fmt.Sprintf("cycle-service-nats-%d", i), rec, req)
		}

		service.Stop()

		if i > 0 && i%10 == 0 {
			runtime.GC()
			profiler.Snapshot(fmt.Sprintf("cycle_%d", i))
		}
	}

	runtime.GC()
	profiler.Snapshot("final")

	profiler.AssertNoGrowth(30.0)
}

// TestMemoryRelease_LargePayload_Local verifies buffers are released after large requests
func TestMemoryRelease_LargePayload_Local(t *testing.T) {
	transport := localtransport.New()

	gateway := zephyr.NewGateway("test-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	service := setupTestService(transport, "large-payload-service")
	defer service.Stop()

	profiler := NewMemoryProfiler(t, "large_payload_local")

	// Generate 1MB payload
	payloadSize := 1024 * 1024
	payload := bytes.Repeat([]byte("x"), payloadSize)

	// Warmup
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/test", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		transport.Dispatch("large-payload-service", rec, req)
	}

	runtime.GC()
	runtime.GC()
	profiler.Snapshot("after_warmup")

	// Execute requests with large payloads
	numRequests := 20
	for i := 0; i < numRequests; i++ {
		req := httptest.NewRequest("POST", "/test", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		transport.Dispatch("large-payload-service", rec, req)
	}

	// Force GC
	runtime.GC()
	runtime.GC()
	profiler.Snapshot("after_requests")

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Heap should not retain all the payload data
	// Allow for ~5MB overhead after 20x1MB requests
	maxExpectedHeap := uint64(20 * 1024 * 1024)
	if m.HeapAlloc > maxExpectedHeap {
		t.Errorf("Heap allocation too high after large payloads: %d bytes (expected < %d)",
			m.HeapAlloc, maxExpectedHeap)
		profiler.WriteHeapProfile("large_payload_retained")
	}
}

// TestMemoryRelease_LargePayload_NATS verifies buffers are released with NATS transport
func TestMemoryRelease_LargePayload_NATS(t *testing.T) {
	nc := setupNATSConnection(t)
	defer nc.Close()

	transport := natstransport.New(nc)

	gateway := zephyr.NewGateway("test-gateway", transport)
	gateway.Start()
	defer gateway.Stop()

	service := setupTestService(transport, "large-payload-service-nats")
	defer service.Stop()

	time.Sleep(100 * time.Millisecond)

	profiler := NewMemoryProfiler(t, "large_payload_nats")

	// Generate 100KB payload (smaller for NATS to avoid message size limits)
	payloadSize := 100 * 1024
	payload := bytes.Repeat([]byte("x"), payloadSize)

	// Warmup
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/test", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		transport.Dispatch("large-payload-service-nats", rec, req)
	}

	runtime.GC()
	runtime.GC()
	profiler.Snapshot("after_warmup")

	// Execute requests with large payloads
	numRequests := 20
	for i := 0; i < numRequests; i++ {
		req := httptest.NewRequest("POST", "/test", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		transport.Dispatch("large-payload-service-nats", rec, req)
	}

	// Force GC
	runtime.GC()
	runtime.GC()
	profiler.Snapshot("after_requests")

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Allow for ~10MB overhead after 20x100KB requests
	maxExpectedHeap := uint64(15 * 1024 * 1024)
	if m.HeapAlloc > maxExpectedHeap {
		t.Errorf("Heap allocation too high after large payloads: %d bytes (expected < %d)",
			m.HeapAlloc, maxExpectedHeap)
		profiler.WriteHeapProfile("large_payload_retained")
	}
}
