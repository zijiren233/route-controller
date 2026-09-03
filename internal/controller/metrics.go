package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type controllerMetrics struct {
	reconciliations *prometheus.CounterVec
	duration        prometheus.Histogram
	routeActions    *prometheus.CounterVec
	desiredRoutes   prometheus.Gauge
	healthyWorkers  prometheus.Gauge
	lastSuccess     prometheus.Gauge
}

var defaultControllerMetrics = newMetrics()

func newMetrics() *controllerMetrics {
	values := &controllerMetrics{
		reconciliations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "route_controller_reconciliations_total",
			Help: "Total number of route reconciliation attempts.",
		}, []string{"result"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "route_controller_reconcile_duration_seconds",
			Help:    "Duration of route reconciliation attempts in seconds.",
			Buckets: prometheus.DefBuckets,
		}),
		routeActions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "route_controller_route_actions_total",
			Help: "Total number of planned or applied route actions.",
		}, []string{"action", "mode"}),
		desiredRoutes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "route_controller_desired_routes",
			Help: "Current number of desired managed routes.",
		}),
		healthyWorkers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "route_controller_healthy_workers",
			Help: "Current number of workers eligible for Service CIDR routing.",
		}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "route_controller_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful active reconciliation.",
		}),
	}
	metrics.Registry.MustRegister(
		values.reconciliations,
		values.duration,
		values.routeActions,
		values.desiredRoutes,
		values.healthyWorkers,
		values.lastSuccess,
	)

	return values
}
