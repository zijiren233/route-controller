package kubecache_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ciliumv2 "github.com/zijiren233/route-controller/internal/apis/cilium/v2"
	"github.com/zijiren233/route-controller/internal/kubecache"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestRouteChanges(t *testing.T) {
	t.Parallel()

	node := &corev1.Node{Status: corev1.NodeStatus{
		Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.1"}},
		Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
	}}
	pod := &corev1.Pod{Spec: corev1.PodSpec{NodeName: "worker-a"}, Status: corev1.PodStatus{
		Phase:      corev1.PodRunning,
		Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
	}}
	ciliumNode := &ciliumv2.CiliumNode{Spec: ciliumv2.CiliumNodeSpec{
		Addresses: []ciliumv2.NodeAddress{{Type: "InternalIP", IP: "192.0.2.1"}},
		IPAM:      ciliumv2.IPAMSpec{PodCIDRs: []string{"10.0.1.0/24"}},
		Health:    ciliumv2.HealthAddress{IPv4: "10.0.1.2"},
	}}

	tests := []struct {
		name    string
		object  client.Object
		mutate  func(*testing.T, client.Object)
		changed bool
	}{
		{"node heartbeat", node, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*corev1.Node](t, o).Status.Conditions[0].LastHeartbeatTime = metav1.Now()
		}, false},
		{"node images", node, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*corev1.Node](t, o).Status.Images = []corev1.ContainerImage{
				{Names: []string{"unused"}},
			}
		}, false},
		{"node ready", node, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*corev1.Node](t, o).Status.Conditions[0].Status = corev1.ConditionFalse
		}, true},
		{"node address", node, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*corev1.Node](t, o).Status.Addresses[0].Address = "192.0.2.2"
		}, true},
		{"node deleting", node, markDeleting, true},
		{"pod deleting", pod, markDeleting, true},
		{"pod ready", pod, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*corev1.Pod](t, o).Status.Conditions[0].Status = corev1.ConditionFalse
		}, true},
		{"pod phase", pod, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*corev1.Pod](t, o).Status.Phase = corev1.PodFailed
		}, true},
		{"pod binding", pod, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*corev1.Pod](t, o).Spec.NodeName = "worker-b"
		}, true},
		{"pod message", pod, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*corev1.Pod](t, o).Status.Conditions[0].Message = "unused"
		}, false},
		{"cilium address", ciliumNode, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*ciliumv2.CiliumNode](t, o).Spec.Addresses[0].IP = "192.0.2.2"
		}, true},
		{"cilium allocation", ciliumNode, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*ciliumv2.CiliumNode](t, o).Spec.IPAM.PodCIDRs[0] = "10.0.2.0/24"
		}, true},
		{"cilium health", ciliumNode, func(t *testing.T, o client.Object) {
			t.Helper()

			as[*ciliumv2.CiliumNode](t, o).Spec.Health.IPv4 = "10.0.1.3"
		}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			old := as[client.Object](t, test.object.DeepCopyObject())
			current := as[client.Object](t, test.object.DeepCopyObject())
			test.mutate(t, current)
			current.SetResourceVersion("2")

			old = applyTransform(t, old)
			current = applyTransform(t, current)
			beforeOld, beforeCurrent := old.DeepCopyObject(), current.DeepCopyObject()
			assert.Equal(t, test.changed, kubecache.RouteChanges().Update(event.UpdateEvent{
				ObjectOld: old, ObjectNew: current,
			}))
			assert.Equal(t, beforeOld, old, "predicate must not mutate informer objects")
			assert.Equal(t, beforeCurrent, current, "predicate must not mutate informer objects")
		})
	}

	for _, object := range []client.Object{node, pod, ciliumNode} {
		old := as[client.Object](t, object.DeepCopyObject())
		current := as[client.Object](t, object.DeepCopyObject())
		current.SetResourceVersion("2")
		current.SetAnnotations(map[string]string{"unused": "changed"})

		old = applyTransform(t, old)
		current = applyTransform(t, current)
		filter := kubecache.RouteChanges()
		assert.False(t, filter.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: current}))
		assert.False(
			t,
			filter.Update(event.UpdateEvent{ObjectOld: current, ObjectNew: current}),
			"resync",
		)
		assert.True(t, filter.Create(event.CreateEvent{Object: current}))
		assert.True(t, filter.Delete(event.DeleteEvent{Object: current}))
		assert.True(t, filter.Generic(event.GenericEvent{Object: current}))
	}
}

func markDeleting(_ *testing.T, object client.Object) {
	value := metav1.NewTime(time.Unix(1, 0))
	object.SetDeletionTimestamp(&value)
}

func as[T any](t *testing.T, object any) T {
	t.Helper()

	value, ok := object.(T)
	require.True(t, ok)

	return value
}
