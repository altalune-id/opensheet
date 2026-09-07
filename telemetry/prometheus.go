package telemetry

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"altalune.id/template/worker"
)

// PrometheusHandler returns an http.Handler serving the /metrics endpoint.
func PrometheusHandler() http.Handler { return promhttp.Handler() }

// PrometheusWorker returns a worker.Worker serving /metrics on cfg.Addr at cfg.Path.
func PrometheusWorker(cfg PrometheusConfig, log *slog.Logger) worker.Worker {
	mux := http.NewServeMux()
	mux.Handle(cfg.Path, PrometheusHandler())
	return worker.HTTP("prometheus", cfg.Addr, mux, log)
}
