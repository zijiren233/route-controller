// Package app wires the controller-runtime manager and production adapters.
package app

import (
	"context"
	"fmt"
	"net/http"

	ciliumv2 "github.com/zijiren233/route-controller/internal/apis/cilium/v2"
	"github.com/zijiren233/route-controller/internal/config"
	"github.com/zijiren233/route-controller/internal/controller"
	"github.com/zijiren233/route-controller/internal/kubecache"
	"github.com/zijiren233/route-controller/internal/planner"
	"github.com/zijiren233/route-controller/internal/probe"
	"github.com/zijiren233/route-controller/internal/route"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const userAgent = "route-controller"

func Run(ctx context.Context, cfg config.Validated) error {
	restConfig, err := clientcmd.BuildConfigFromFlags("", cfg.Kubernetes.Kubeconfig)
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}

	restConfig.UserAgent = userAgent
	restConfig.QPS = cfg.Kubernetes.QPS
	restConfig.Burst = cfg.Kubernetes.Burst

	backend, err := route.NewNetlinkBackend(
		cfg.Routes.Interface,
		cfg.Routes.Table,
		cfg.Routes.Protocol,
		cfg.PodCIDR,
		cfg.ServiceCIDR,
	)
	if err != nil {
		return err
	}

	apiScheme := runtime.NewScheme()
	if err := scheme.AddToScheme(apiScheme); err != nil {
		return fmt.Errorf("register Kubernetes API scheme: %w", err)
	}

	if err := ciliumv2.AddToScheme(apiScheme); err != nil {
		return fmt.Errorf("register Cilium API scheme: %w", err)
	}

	status := controller.NewStatusStore(cfg.Controller.DryRun)

	manager, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: apiScheme,
		Cache:  kubecache.Options(),
		Metrics: metricsserver.Options{
			BindAddress: cfg.Observability.MetricsBindAddress,
			ExtraHandlers: map[string]http.Handler{
				"/status": status,
			},
		},
		HealthProbeBindAddress: cfg.Observability.HealthProbeBindAddress,
		LeaderElection:         false,
	})
	if err != nil {
		return fmt.Errorf("create controller manager: %w", err)
	}

	reconciler := controller.NewReconciler(
		manager.GetClient(),
		planner.New(cfg.PodCIDR, cfg.ServiceCIDR, cfg.RouterCIDR),
		backend,
		probe.NewHTTP(cfg.Probe.Timeout, cfg.Probe.Port),
		planner.NewProbeTracker(cfg.Probe.FailureThreshold, cfg.Probe.SuccessThreshold),
		status,
		controller.ReconcilerConfig{
			ReconcilePeriod: cfg.Controller.ReconcilePeriod,
			DryRun:          cfg.Controller.DryRun,
		},
	)
	if err := reconciler.SetupWithManager(manager); err != nil {
		return fmt.Errorf("configure route controller: %w", err)
	}

	if err := manager.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("configure liveness check: %w", err)
	}

	if err := manager.AddReadyzCheck("routes", status.Ready); err != nil {
		return fmt.Errorf("configure readiness check: %w", err)
	}

	if err := manager.Start(ctx); err != nil {
		return fmt.Errorf("run controller manager: %w", err)
	}

	return nil
}
