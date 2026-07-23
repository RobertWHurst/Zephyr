package natstransport_test

import (
	"context"
	"testing"
	"time"

	"github.com/RobertWHurst/zephyr"
)

// startService runs service.Listen in the background and waits for the
// service to report ready. Cleanup stops the service and waits for Listen to
// return.
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
	case <-service.Ready():
		tb.Cleanup(func() {
			cancel()
			<-errCh
		})
		return nil
	case <-time.After(5 * time.Second):
		cancel()
		tb.Fatal("service did not become ready")
		return nil
	}
}

// startGateway runs gateway.Connect in the background and waits for the
// gateway to report ready. Cleanup stops the gateway and waits for Connect to
// return.
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
	case <-gateway.Ready():
		tb.Cleanup(func() {
			cancel()
			<-errCh
		})
		return nil
	case <-time.After(5 * time.Second):
		cancel()
		tb.Fatal("gateway did not become ready")
		return nil
	}
}
