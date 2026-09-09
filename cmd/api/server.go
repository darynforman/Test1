package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func (a *application) serve() error {
	srv := &http.Server{Addr: fmt.Sprintf(":%d", a.config.port), Handler: a.routes(), IdleTimeout: time.Minute, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		s := <-quit
		a.logger.Info("caught signal", "signal", s.String())
		ctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		if e := srv.Shutdown(ctx); e != nil {
			done <- e
			return
		}
		done <- nil
	}()
	a.logger.Info("starting server", "addr", srv.Addr, "env", a.config.env)
	e := srv.ListenAndServe()
	if !errors.Is(e, http.ErrServerClosed) {
		return e
	}
	if e = <-done; e != nil {
		return e
	}
	a.logger.Info("stopped server")
	return nil
}
