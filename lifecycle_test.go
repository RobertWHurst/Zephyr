package zephyr_test

import (
	"context"
	"testing"
	"time"

	"github.com/RobertWHurst/zephyr"
	localtransport "github.com/RobertWHurst/zephyr/local-transport"
)

func startService(tb testing.TB, service *zephyr.Service) error {
	tb.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Listen(ctx)
	}()
	select {
	case err := <-errCh:
		cancel()
		return err
	case <-time.After(20 * time.Millisecond):
		tb.Cleanup(func() {
			cancel()
			_ = service.Close()
			<-errCh
		})
		return nil
	}
}

func startGateway(tb testing.TB, gateway *zephyr.Gateway) error {
	tb.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- gateway.Connect(ctx)
	}()
	select {
	case err := <-errCh:
		cancel()
		return err
	case <-time.After(20 * time.Millisecond):
		tb.Cleanup(func() {
			cancel()
			_ = gateway.Close()
			<-errCh
		})
		return nil
	}
}

func watchServiceAnnouncements(tb testing.TB, transport *localtransport.LocalTransport, handler func(*zephyr.ServiceDescriptor)) {
	tb.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- transport.HandleServiceAnnouncements(ctx, ready, handler)
	}()
	select {
	case <-ready:
		tb.Cleanup(func() {
			cancel()
			if err := <-errCh; err != nil {
				tb.Fatalf("service announcement handler failed: %v", err)
			}
		})
	case err := <-errCh:
		cancel()
		tb.Fatalf("service announcement handler failed: %v", err)
	case <-time.After(time.Second):
		cancel()
		tb.Fatal("service announcement handler did not become ready")
	}
}
