package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// shutdownDrainTimeout 停机最长排空时间：超过后强制收尾。
const shutdownDrainTimeout = 20 * time.Second

// App owns the panel's runtime components and shuts them down in dependency
// order: stop accepting HTTP, stop scheduled work, close WebSockets (failing
// their pending commands), wait for background workers, then close the
// database. Each hook is injected so tests can verify ordering without
// starting real components.
type App struct {
	Server        *http.Server
	ShutdownHTTP  func(context.Context) error
	StopScheduler func()
	CloseSessions func(reason string)
	WaitWorkers   func(context.Context) error
	CloseDatabase func() error

	shutdownOnce sync.Once
	shutdownErr  error
}

// Run blocks serving HTTP until the listener fails or Shutdown is called.
// ErrServerClosed (normal shutdown) is not an error.
func (a *App) Run() error {
	if a.Server == nil {
		return errors.New("app has no http server")
	}
	if err := a.Server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown drains all components within shutdownDrainTimeout. Errors are
// joined; a failing component never skips the remaining cleanup. Idempotent.
func (a *App) Shutdown(parent context.Context) error {
	a.shutdownOnce.Do(func() {
		ctx, cancel := context.WithTimeout(parent, shutdownDrainTimeout)
		defer cancel()

		var errs []error
		if a.ShutdownHTTP != nil {
			if err := a.ShutdownHTTP(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if a.StopScheduler != nil {
			a.StopScheduler()
		}
		if a.CloseSessions != nil {
			a.CloseSessions("面板正在停机")
		}
		if a.WaitWorkers != nil {
			if err := a.WaitWorkers(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if a.CloseDatabase != nil {
			if err := a.CloseDatabase(); err != nil {
				errs = append(errs, err)
			}
		}
		a.shutdownErr = errors.Join(errs...)
	})
	return a.shutdownErr
}
