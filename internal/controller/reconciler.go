// Package controller reconciles cached Kubernetes state into local Linux routes.
package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	ciliumv2 "github.com/zijiren233/route-controller/internal/apis/cilium/v2"
	"github.com/zijiren233/route-controller/internal/kubecache"
	"github.com/zijiren233/route-controller/internal/planner"
	"github.com/zijiren233/route-controller/internal/probe"
	"github.com/zijiren233/route-controller/internal/route"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	errNoHealthyWorkers = errors.New("no probe-healthy worker is available for the Service CIDR")
	errDryRun           = errors.New("dry-run mode does not apply routes")
)

type ReconcilerConfig struct {
	ReconcilePeriod time.Duration
	DryRun          bool
}

type Reconciler struct {
	client  client.Reader
	planner *planner.Planner
	backend route.Backend
	prober  probe.Prober
	tracker *planner.ProbeTracker
	status  *StatusStore
	config  ReconcilerConfig
	metrics *controllerMetrics
}

func NewReconciler(
	apiClient client.Reader,
	routePlanner *planner.Planner,
	backend route.Backend,
	prober probe.Prober,
	tracker *planner.ProbeTracker,
	status *StatusStore,
	config ReconcilerConfig,
) *Reconciler {
	return &Reconciler{
		client:  apiClient,
		planner: routePlanner,
		backend: backend,
		prober:  prober,
		tracker: tracker,
		status:  status,
		config:  config,
		metrics: defaultControllerMetrics,
	}
}

func (reconciler *Reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	startedAt := time.Now()

	resultLabel := "success"
	defer func() {
		reconciler.metrics.reconciliations.WithLabelValues(resultLabel).Inc()
		reconciler.metrics.duration.Observe(time.Since(startedAt).Seconds())
	}()

	workers, err := reconciler.readWorkers(ctx)
	if err != nil {
		resultLabel = "error"
		return reconciler.failed(err, nil, nil, nil)
	}

	baseRoutes, err := reconciler.planner.PodRoutes(workers)
	if err != nil {
		resultLabel = "error"
		return reconciler.failed(err, baseRoutes, nil, workers)
	}

	preflightRoutes, err := reconciler.preflightRoutes(baseRoutes, workers)
	if err != nil {
		resultLabel = "error"
		return reconciler.failed(err, baseRoutes, nil, workers)
	}

	if _, err = reconciler.backend.Reconcile(
		ctx,
		preflightRoutes,
		route.ReconcileOptions{DryRun: true},
	); err != nil {
		resultLabel = "error"

		return reconciler.failed(
			fmt.Errorf("preflight managed routes: %w", err),
			preflightRoutes,
			nil,
			workers,
		)
	}

	baseResult, err := reconciler.backend.Reconcile(
		ctx,
		baseRoutes,
		route.ReconcileOptions{DryRun: reconciler.config.DryRun},
	)
	if err != nil {
		resultLabel = "error"

		return reconciler.failed(
			fmt.Errorf("reconcile PodCIDR routes: %w", err),
			baseRoutes,
			baseResult.Actions,
			workers,
		)
	}

	reconciler.probeWorkers(ctx, workers)

	serviceRoute, err := reconciler.planner.ServiceRoute(workers)
	if err != nil {
		resultLabel = "error"
		return reconciler.failed(err, baseRoutes, baseResult.Actions, workers)
	}

	desired := slices.Clone(baseRoutes)
	if serviceRoute != nil {
		desired = append(desired, *serviceRoute)
	}

	route.Sort(desired)

	finalResult, err := reconciler.backend.Reconcile(ctx, desired, route.ReconcileOptions{
		Prune:  true,
		DryRun: reconciler.config.DryRun,
	})

	actions := uniqueActions(append(baseResult.Actions, finalResult.Actions...))
	if err != nil {
		resultLabel = "error"

		return reconciler.failed(
			fmt.Errorf("reconcile managed routes: %w", err),
			desired,
			actions,
			workers,
		)
	}

	reconciler.updateMetrics(desired, actions, workers)

	switch {
	case reconciler.config.DryRun:
		resultLabel = "dry_run"

		reconciler.status.Record(false, errDryRun, desired, actions, workers)
	case serviceRoute == nil:
		resultLabel = "degraded"

		reconciler.status.Record(false, errNoHealthyWorkers, desired, actions, workers)
		log.FromContext(ctx).Info("no healthy Service CIDR next hop", "workers", len(workers))
	default:
		reconciler.status.Record(true, nil, desired, actions, workers)
		reconciler.metrics.lastSuccess.SetToCurrentTime()
		log.FromContext(ctx).Info(
			"route reconciliation complete",
			"routes",
			len(desired),
			"actions",
			len(actions),
		)
	}

	return ctrl.Result{RequeueAfter: reconciler.config.ReconcilePeriod}, nil
}

func (reconciler *Reconciler) readWorkers(ctx context.Context) ([]planner.Worker, error) {
	var nodes corev1.NodeList
	if err := reconciler.client.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("list Nodes from cache: %w", err)
	}

	var pods corev1.PodList
	if err := reconciler.client.List(
		ctx,
		&pods,
		client.InNamespace(metav1.NamespaceSystem),
		client.MatchingLabels{kubecache.CiliumPodLabelKey: kubecache.CiliumPodLabelValue},
	); err != nil {
		return nil, fmt.Errorf("list Cilium Pods from cache: %w", err)
	}

	var ciliumNodes ciliumv2.CiliumNodeList
	if err := reconciler.client.List(ctx, &ciliumNodes); err != nil {
		return nil, fmt.Errorf("list CiliumNodes from cache: %w", err)
	}

	states := make([]planner.CiliumNodeState, 0, len(ciliumNodes.Items))
	for _, item := range ciliumNodes.Items {
		state := planner.CiliumNodeState{
			Name:     item.Name,
			HealthIP: item.Spec.Health.IPv4,
			PodCIDRs: slices.Clone(item.Spec.IPAM.PodCIDRs),
		}
		for _, address := range item.Spec.Addresses {
			if address.Type == string(corev1.NodeInternalIP) {
				state.NodeIP = address.IP
				break
			}
		}

		states = append(states, state)
	}

	return reconciler.planner.Workers(nodes.Items, pods.Items, states), nil
}

func (reconciler *Reconciler) preflightRoutes(
	baseRoutes []route.Desired,
	workers []planner.Worker,
) ([]route.Desired, error) {
	preflightWorkers := cloneWorkers(workers)
	for index := range preflightWorkers {
		preflightWorkers[index].ProbeReady = preflightWorkers[index].APIReady
	}

	serviceRoute, err := reconciler.planner.ServiceRoute(preflightWorkers)
	if err != nil {
		return nil, err
	}

	preflight := slices.Clone(baseRoutes)
	if serviceRoute != nil {
		preflight = append(preflight, *serviceRoute)
	}

	return preflight, nil
}

func (reconciler *Reconciler) probeWorkers(ctx context.Context, workers []planner.Worker) {
	var group sync.WaitGroup

	names := make(map[string]struct{}, len(workers))
	for index := range workers {
		names[workers[index].Name] = struct{}{}
		if !workers[index].APIReady {
			workers[index].ProbeReady = reconciler.tracker.Update(workers[index].Name, false, false)
			continue
		}

		group.Add(1)
		go func(worker *planner.Worker) {
			defer group.Done()

			healthy := reconciler.prober.Healthy(ctx, worker.HealthIP)
			worker.ProbeReady = reconciler.tracker.Update(worker.Name, true, healthy)
		}(&workers[index])
	}

	group.Wait()
	reconciler.tracker.Retain(names)
}

func (reconciler *Reconciler) failed(
	err error,
	desired []route.Desired,
	actions []route.Action,
	workers []planner.Worker,
) (ctrl.Result, error) {
	reconciler.status.Record(false, err, desired, actions, workers)
	log.Log.Error(err, "route reconciliation failed")
	return ctrl.Result{}, err
}

func (reconciler *Reconciler) updateMetrics(
	desired []route.Desired,
	actions []route.Action,
	workers []planner.Worker,
) {
	reconciler.metrics.desiredRoutes.Set(float64(len(desired)))

	healthyWorkers := 0
	for _, worker := range workers {
		if worker.APIReady && worker.ProbeReady {
			healthyWorkers++
		}
	}

	reconciler.metrics.healthyWorkers.Set(float64(healthyWorkers))

	mode := "active"
	if reconciler.config.DryRun {
		mode = "dry_run"
	}

	for _, action := range actions {
		reconciler.metrics.routeActions.WithLabelValues(string(action.Type), mode).Inc()
	}
}

func uniqueActions(actions []route.Action) []route.Action {
	seen := make(map[string]struct{}, len(actions))

	result := make([]route.Action, 0, len(actions))
	for _, action := range actions {
		key := action.String()
		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}

		result = append(result, action)
	}

	slices.SortFunc(result, func(first, second route.Action) int {
		return cmp.Compare(first.String(), second.String())
	})

	return result
}
