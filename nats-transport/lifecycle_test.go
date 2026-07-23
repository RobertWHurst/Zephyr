package natstransport_test

import (
	"context"
	"testing"
	"time"

	"github.com/RobertWHurst/zephyr"
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
