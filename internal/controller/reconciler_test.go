package controller_test

import (
	"context"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ciliumv2 "github.com/zijiren233/route-controller/internal/apis/cilium/v2"
	"github.com/zijiren233/route-controller/internal/controller"
	"github.com/zijiren233/route-controller/internal/planner"
	"github.com/zijiren233/route-controller/internal/route"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileUsesTwoPhaseRouting(t *testing.T) {
	apiScheme := testScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(apiScheme).WithObjects(testObjects()...).Build()
	backend := &recordingBackend{}
	status := controller.NewStatusStore(false)
	reconciler := controller.NewReconciler(
		apiClient,
		testPlanner(),
		backend,
		staticProber(true),
		planner.NewProbeTracker(3, 1),
		status,
		controller.ReconcilerConfig{ReconcilePeriod: time.Minute},
	)

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{})
	require.NoError(t, err)
	assert.Equal(t, time.Minute, result.RequeueAfter)
	require.Len(t, backend.calls, 3)
	assert.True(t, backend.calls[0].options.DryRun, "preflight must be read-only")
	assert.Len(
		t,
		backend.calls[0].desired,
		2,
		"preflight checks PodCIDR and Service CIDR conflicts",
	)
	assert.False(t, backend.calls[1].options.Prune, "PodCIDR routes are installed before probing")
	assert.Len(t, backend.calls[1].desired, 1)
	assert.True(t, backend.calls[2].options.Prune)
	assert.Len(t, backend.calls[2].desired, 2)
	assert.True(t, status.Snapshot().Ready)
}

func TestReconcileWithdrawsServiceRouteWithoutHealthyWorker(t *testing.T) {
	apiClient := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(testObjects()...).
		Build()
	backend := &recordingBackend{}
	status := controller.NewStatusStore(false)
	reconciler := controller.NewReconciler(
		apiClient,
		testPlanner(),
		backend,
		staticProber(false),
		planner.NewProbeTracker(1, 1),
		status,
		controller.ReconcilerConfig{ReconcilePeriod: time.Minute},
	)

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{})
	require.NoError(t, err)
	require.Len(t, backend.calls, 3)
	assert.True(t, backend.calls[2].options.Prune)
	assert.Len(t, backend.calls[2].desired, 1)

	snapshot := status.Snapshot()
	assert.False(t, snapshot.Ready)
	assert.ErrorContains(t, status.Ready(nil), "no probe-healthy worker")
}

func TestReconcileStopsAfterPreflightConflict(t *testing.T) {
	apiClient := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(testObjects()...).
		Build()
	backend := &recordingBackend{failure: assert.AnError}
	reconciler := controller.NewReconciler(
		apiClient,
		testPlanner(),
		backend,
		staticProber(true),
		planner.NewProbeTracker(1, 1),
		controller.NewStatusStore(false),
		controller.ReconcilerConfig{ReconcilePeriod: time.Minute},
	)

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{})
	require.Error(t, err)
	assert.Len(t, backend.calls, 1)
	assert.True(t, backend.calls[0].options.DryRun)
}

type staticProber bool

func (healthy staticProber) Healthy(context.Context, netip.Addr) bool {
	return bool(healthy)
}

type backendCall struct {
	desired []route.Desired
	options route.ReconcileOptions
}

type recordingBackend struct {
	calls   []backendCall
	failure error
}

func (backend *recordingBackend) Reconcile(
	_ context.Context,
	desired []route.Desired,
	options route.ReconcileOptions,
) (route.Result, error) {
	cloned := make([]route.Desired, len(desired))
	for index := range desired {
		cloned[index] = desired[index]
		cloned[index].Gateways = slices.Clone(desired[index].Gateways)
	}

	backend.calls = append(backend.calls, backendCall{desired: cloned, options: options})
	if backend.failure != nil {
		return route.Result{}, backend.failure
	}

	return route.Result{}, nil
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, ciliumv2.AddToScheme(scheme))

	return scheme
}

func testPlanner() *planner.Planner {
	return planner.New(
		netip.MustParsePrefix("10.0.0.0/10"),
		netip.MustParsePrefix("10.192.0.0/12"),
		netip.MustParsePrefix("192.0.2.0/24"),
	)
}

func testObjects() []client.Object {
	return []client.Object{
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
			Status: corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{Type: corev1.NodeInternalIP, Address: "192.0.2.190"},
				},
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cilium-worker-1", Namespace: metav1.NamespaceSystem,
				Labels: map[string]string{"k8s-app": "cilium"},
			},
			Spec: corev1.PodSpec{NodeName: "worker-1"},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionTrue},
				},
			},
		},
		&ciliumv2.CiliumNode{
			ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
			Spec: ciliumv2.CiliumNodeSpec{
				Addresses: []ciliumv2.NodeAddress{{Type: "InternalIP", IP: "192.0.2.190"}},
				Health:    ciliumv2.HealthAddress{IPv4: "10.0.3.161"},
				IPAM:      ciliumv2.IPAMSpec{PodCIDRs: []string{"10.0.3.0/24"}},
			},
		},
	}
}
