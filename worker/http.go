package worker

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// HTTP returns a Worker serving h on addr with sane timeouts and graceful shutdown.
func HTTP(name, addr string, h http.Handler, log *slog.Logger) Worker {
	if log == nil {
		log = slog.Default()
	}
	return &httpWorker{name: name, addr: addr, handler: h, log: log}
}

type httpWorker struct {
	name    string
	addr    string
	handler http.Handler
	log     *slog.Logger
}

func (w *httpWorker) Name() string { return w.name }

func (w *httpWorker) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	srv := &http.Server{
		Addr:              w.addr,
		Handler:           w.handler,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(l net.Listener) context.Context { return runCtx },
	}

	ln, err := net.Listen("tcp", w.addr)
	if err != nil {
		return err
	}
	w.log.Info("http worker listening", slog.String("worker", w.name), slog.String("addr", ln.Addr().String()))

	errCh := make(chan error, 1)
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
			w.log.Error("http worker shutdown", slog.String("worker", w.name), slog.Any("err", shutdownErr))
			return shutdownErr
		}
		<-errCh
		return nil
	case serveErr := <-errCh:
		return serveErr
	}
}
