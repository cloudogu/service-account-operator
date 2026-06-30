package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitForGracefulShutdown(t *testing.T) {
	t.Run("returns nil when manager stops normally", func(t *testing.T) {
		managerErrCh := make(chan error, 1)
		managerErrCh <- nil

		err := waitForGracefulShutdown(context.Background(), time.Second, managerErrCh, nil)

		require.NoError(t, err)
	})

	t.Run("returns wrapped start error", func(t *testing.T) {
		startErr := errors.New("boom")
		managerErrCh := make(chan error, 1)
		managerErrCh <- startErr

		err := waitForGracefulShutdown(context.Background(), time.Second, managerErrCh, nil)

		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to run manager")
		assert.ErrorIs(t, err, startErr)
	})

	t.Run("returns nil when manager stops after shutdown", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		managerErrCh := make(chan error, 1)
		startedCh := make(chan struct{})
		errCh := make(chan error, 1)
		var sareCleanupCalled bool
		go func() {
			close(startedCh)
			errCh <- waitForGracefulShutdown(ctx, time.Second, managerErrCh, func() { sareCleanupCalled = true })
		}()

		<-startedCh

		cancel()

		// Give waitForGracefulShutdown a chance to observe the shutdown signal first so this
		// assertion exercises the graceful shutdown path instead of the initial manager exit path.
		time.Sleep(10 * time.Millisecond)
		managerErrCh <- context.Canceled

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for graceful shutdown")
		}
		assert.True(t, sareCleanupCalled)
	})

	t.Run("returns timeout when manager does not stop", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		timeout := 20 * time.Millisecond
		managerErrCh := make(chan error)
		errCh := make(chan error, 1)
		sareCleanupCalled := false

		go func() {
			errCh <- waitForGracefulShutdown(ctx, timeout, managerErrCh, func() { sareCleanupCalled = true })
		}()
		cancel()

		select {
		case err := <-errCh:
			require.Error(t, err)
			assert.ErrorContains(t, err, "graceful shutdown timed out")
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for shutdown timeout")
		}
		assert.True(t, sareCleanupCalled)
	})
}
