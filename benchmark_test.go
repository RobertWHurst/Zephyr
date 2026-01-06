package zephyr_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/RobertWHurst/navaros"
	"github.com/RobertWHurst/zephyr"
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
		nats.Name(fmt.Sprintf("zephyr-test-%s", tb.Name())),
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

// simpleHandler is a basic handler for testing
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

// setupTestService creates and starts a test service with routes
func setupTestService(transport zephyr.Transport, name string) *zephyr.Service {
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

func mustRouteDescriptor(method, pattern string) *zephyr.RouteDescriptor {
	rd, err := zephyr.NewRouteDescriptor(method, pattern)
	if err != nil {
		panic(err)
	}
	return rd
}

// MemoryProfiler wraps heap profiling for tests
type MemoryProfiler struct {
	tb          testing.TB
	profileDir  string
	profileName string
	snapshots   []runtime.MemStats
}

// NewMemoryProfiler creates a new memory profiler
func NewMemoryProfiler(tb testing.TB, name string) *MemoryProfiler {
	profileDir := os.Getenv("ZEPHYR_PROFILE_DIR")
	if profileDir == "" {
		profileDir = "/tmp/zephyr-profiles"
	}
	os.MkdirAll(profileDir, 0755)

	return &MemoryProfiler{
		tb:          tb,
		profileDir:  profileDir,
		profileName: name,
		snapshots:   make([]runtime.MemStats, 0),
	}
}

// Snapshot takes a memory snapshot
func (mp *MemoryProfiler) Snapshot(label string) {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	mp.snapshots = append(mp.snapshots, m)

	mp.tb.Logf("[%s] Snapshot %s: HeapAlloc=%d, HeapInuse=%d, NumGC=%d, Goroutines=%d",
		mp.profileName, label, m.HeapAlloc, m.HeapInuse, m.NumGC, runtime.NumGoroutine())
}

// WriteHeapProfile writes a heap profile to file
func (mp *MemoryProfiler) WriteHeapProfile(suffix string) string {
	filename := filepath.Join(mp.profileDir, mp.profileName+"_"+suffix+".heap")
	f, err := os.Create(filename)
	if err != nil {
		mp.tb.Logf("Failed to create heap profile: %v", err)
		return ""
	}
	defer f.Close()

	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		mp.tb.Logf("Failed to write heap profile: %v", err)
		return ""
	}
	mp.tb.Logf("Wrote heap profile to %s", filename)
	return filename
}

// WriteGoroutineProfile writes a goroutine profile to file
func (mp *MemoryProfiler) WriteGoroutineProfile(suffix string) string {
	filename := filepath.Join(mp.profileDir, mp.profileName+"_"+suffix+".goroutine")
	f, err := os.Create(filename)
	if err != nil {
		mp.tb.Logf("Failed to create goroutine profile: %v", err)
		return ""
	}
	defer f.Close()

	if err := pprof.Lookup("goroutine").WriteTo(f, 1); err != nil {
		mp.tb.Logf("Failed to write goroutine profile: %v", err)
		return ""
	}
	mp.tb.Logf("Wrote goroutine profile to %s", filename)
	return filename
}

// AssertNoGrowth verifies memory did not grow significantly between first and last snapshots
func (mp *MemoryProfiler) AssertNoGrowth(maxGrowthPercent float64) bool {
	if len(mp.snapshots) < 2 {
		mp.tb.Log("Not enough snapshots to compare")
		return true
	}

	first := mp.snapshots[0]
	last := mp.snapshots[len(mp.snapshots)-1]

	if first.HeapAlloc == 0 {
		return true
	}

	growth := float64(last.HeapAlloc-first.HeapAlloc) / float64(first.HeapAlloc) * 100

	if growth > maxGrowthPercent {
		mp.tb.Errorf("Memory grew by %.2f%% (HeapAlloc: %d -> %d), max allowed: %.2f%%",
			growth, first.HeapAlloc, last.HeapAlloc, maxGrowthPercent)
		mp.WriteHeapProfile("leak_detected")
		return false
	}
	return true
}

// CheckGoroutineLeak verifies no goroutine leak occurred
func (mp *MemoryProfiler) CheckGoroutineLeak(initialCount int, maxGrowth int) bool {
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	finalCount := runtime.NumGoroutine()

	if finalCount-initialCount > maxGrowth {
		mp.tb.Errorf("Goroutine leak detected: started with %d, ended with %d (leaked %d)",
			initialCount, finalCount, finalCount-initialCount)
		mp.WriteGoroutineProfile("goroutine_leak")
		return false
	}
	return true
}

// GetMemoryGrowthSlope calculates the memory growth trend from snapshots
func (mp *MemoryProfiler) GetMemoryGrowthSlope() float64 {
	if len(mp.snapshots) < 3 {
		return 0
	}

	samples := make([]uint64, len(mp.snapshots))
	for i, s := range mp.snapshots {
		samples[i] = s.HeapAlloc
	}

	return calculateMemorySlope(samples)
}

// calculateMemorySlope calculates linear regression slope for memory samples
func calculateMemorySlope(samples []uint64) float64 {
	n := float64(len(samples))
	var sumX, sumY, sumXY, sumX2 float64

	for i, y := range samples {
		x := float64(i)
		sumX += x
		sumY += float64(y)
		sumXY += x * float64(y)
		sumX2 += x * x
	}

	denominator := n*sumX2 - sumX*sumX
	if denominator == 0 {
		return 0
	}

	return (n*sumXY - sumX*sumY) / denominator
}

// assertMemoryStable verifies memory samples do not show consistent growth
func assertMemoryStable(tb testing.TB, samples []uint64) {
	tb.Helper()

	if len(samples) < 3 {
		return
	}

	growthCount := 0
	for i := 1; i < len(samples); i++ {
		if samples[i] > samples[i-1] {
			growthCount++
		}
	}

	growthRatio := float64(growthCount) / float64(len(samples)-1)
	if growthRatio > 0.7 {
		tb.Errorf("Memory appears to be growing consistently (%.0f%% of samples show increase)",
			growthRatio*100)
	}
}
