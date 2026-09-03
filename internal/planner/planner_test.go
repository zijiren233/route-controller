package planner_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zijiren233/route-controller/internal/planner"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPlannerUsesCiliumNodePodCIDR(t *testing.T) {
	t.Parallel()

	routePlanner := testPlanner()
	node := readyNode("worker-1", "192.0.2.190")
	node.Spec.PodCIDR = "10.0.99.0/24"
	workers := routePlanner.Workers(
		[]corev1.Node{node},
		[]corev1.Pod{readyCiliumPod("worker-1")},
		[]planner.CiliumNodeState{{
			Name: "worker-1", NodeIP: "192.0.2.190", HealthIP: "10.0.3.161",
			PodCIDRs: []string{"10.0.3.0/24"},
		}},
	)

	routes, err := routePlanner.PodRoutes(workers)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	assert.Equal(t, netip.MustParsePrefix("10.0.3.0/24"), routes[0].Destination)
}

func TestPlannerRejectsUnsafeWorkerData(t *testing.T) {
	t.Parallel()

	routePlanner := testPlanner()
	node := readyNode("worker-1", "192.0.2.190")
	pod := readyCiliumPod("worker-1")

	tests := map[string]planner.CiliumNodeState{
		"node IP outside router CIDR": {
			Name:     "worker-1",
			NodeIP:   "203.0.113.1",
			HealthIP: "10.0.3.1",
			PodCIDRs: []string{"10.0.3.0/24"},
		},
		"PodCIDR outside allowed range": {
			Name:     "worker-1",
			NodeIP:   "192.0.2.190",
			HealthIP: "172.16.0.1",
			PodCIDRs: []string{"172.16.0.0/16"},
		},
		"health IP outside PodCIDR": {
			Name:     "worker-1",
			NodeIP:   "192.0.2.190",
			HealthIP: "10.0.8.1",
			PodCIDRs: []string{"10.0.3.0/24"},
		},
	}
	for name, state := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			workers := routePlanner.Workers(
				[]corev1.Node{node},
				[]corev1.Pod{pod},
				[]planner.CiliumNodeState{state},
			)
			require.Len(t, workers, 1)
			assert.False(t, workers[0].APIReady)
			assert.NotEqual(t, planner.WorkerStateReady, workers[0].State)
		})
	}
}

func TestServiceRouteUsesProbeHealthyWorkers(t *testing.T) {
	t.Parallel()

	wanted, err := testPlanner().ServiceRoute([]planner.Worker{
		{
			Name:       "worker-1",
			NodeIP:     netip.MustParseAddr("192.0.2.190"),
			APIReady:   true,
			ProbeReady: true,
		},
		{
			Name:       "worker-2",
			NodeIP:     netip.MustParseAddr("192.0.2.191"),
			APIReady:   true,
			ProbeReady: false,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, wanted)
	assert.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.190")}, wanted.Gateways)
}

func TestPodRoutesRejectOverlappingAllocations(t *testing.T) {
	t.Parallel()

	_, err := testPlanner().PodRoutes([]planner.Worker{
		{
			Name: "worker-1", NodeIP: netip.MustParseAddr("192.0.2.190"),
			PodCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.3.0/24")}, APIReady: true,
		},
		{
			Name: "worker-2", NodeIP: netip.MustParseAddr("192.0.2.191"),
			PodCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.3.128/25")}, APIReady: true,
		},
	})
	assert.ErrorContains(t, err, "overlap")
}

func TestProbeTrackerThresholds(t *testing.T) {
	t.Parallel()

	tracker := planner.NewProbeTracker(3, 1)
	assert.True(t, tracker.Update("worker-1", true, true))
	assert.True(t, tracker.Update("worker-1", true, false))
	assert.True(t, tracker.Update("worker-1", true, false))
	assert.False(t, tracker.Update("worker-1", true, false))
	assert.True(t, tracker.Update("worker-1", true, true))
	assert.False(t, tracker.Update("worker-1", false, true))
}

func testPlanner() *planner.Planner {
	return planner.New(
		netip.MustParsePrefix("10.0.0.0/10"),
		netip.MustParsePrefix("10.192.0.0/12"),
		netip.MustParsePrefix("192.0.2.0/24"),
	)
}

func readyNode(name, address string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: address}},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func readyCiliumPod(node string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium-" + node,
			Namespace: metav1.NamespaceSystem,
			Labels:    map[string]string{"k8s-app": "cilium"},
		},
		Spec: corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
}
