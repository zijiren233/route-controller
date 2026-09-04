package kubecache_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ciliumv2 "github.com/zijiren233/route-controller/internal/apis/cilium/v2"
	"github.com/zijiren233/route-controller/internal/kubecache"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	controllercache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestOptionsRestrictCachedObjects(t *testing.T) {
	t.Parallel()

	options := kubecache.Options()

	assert.True(t, options.ReaderFailOnMissingInformer)
	assert.Len(t, options.ByObject, 3)
}

func TestNodeProjection(t *testing.T) {
	t.Parallel()

	deletingAt := metav1.NewTime(time.Unix(10, 0))
	node := &corev1.Node{
		ObjectMeta: fullObjectMeta("worker-a", "", &deletingAt),
		Spec:       corev1.NodeSpec{PodCIDR: "10.1.0.0/24"},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeHostName, Address: "worker-a"},
				{Type: corev1.NodeInternalIP, Address: "192.0.2.10"},
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse, Message: "unused"},
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue, Message: "unused"},
			},
		},
	}

	projected := applyTransform(t, node)

	assert.Same(t, node, projected)
	assert.Equal(t, metav1.ObjectMeta{
		Name:              "worker-a",
		ResourceVersion:   "42",
		DeletionTimestamp: &deletingAt,
	}, projected.ObjectMeta)
	assert.Empty(t, projected.Spec)
	assert.Equal(t, []corev1.NodeAddress{
		{Type: corev1.NodeInternalIP, Address: "192.0.2.10"},
	}, projected.Status.Addresses)
	assert.Equal(t, []corev1.NodeCondition{
		{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
	}, projected.Status.Conditions)
}

func TestPodProjectionPreservesCacheSelector(t *testing.T) {
	t.Parallel()

	deletingAt := metav1.NewTime(time.Unix(20, 0))
	pod := &corev1.Pod{
		ObjectMeta: fullObjectMeta("cilium-a", metav1.NamespaceSystem, &deletingAt),
		Spec: corev1.PodSpec{
			NodeName:   "worker-a",
			Containers: []corev1.Container{{Name: "cilium-agent", Image: "unused"}},
		},
		Status: corev1.PodStatus{
			Phase:   corev1.PodRunning,
			PodIP:   "10.1.0.2",
			Message: "unused",
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, Message: "unused"},
				{Type: corev1.PodReady, Status: corev1.ConditionTrue, Message: "unused"},
			},
		},
	}

	projected := applyTransform(t, pod)

	assert.Equal(t, metav1.ObjectMeta{
		Name:              "cilium-a",
		Namespace:         metav1.NamespaceSystem,
		ResourceVersion:   "42",
		DeletionTimestamp: &deletingAt,
		Labels: map[string]string{
			kubecache.CiliumPodLabelKey: kubecache.CiliumPodLabelValue,
		},
	}, projected.ObjectMeta)
	assert.Equal(t, corev1.PodSpec{NodeName: "worker-a"}, projected.Spec)
	assert.Equal(t, corev1.PodRunning, projected.Status.Phase)
	assert.Equal(t, []corev1.PodCondition{
		{Type: corev1.PodReady, Status: corev1.ConditionTrue},
	}, projected.Status.Conditions)
	assert.Empty(t, projected.Status.PodIP)

	options := optionsFor(t, &corev1.Pod{})
	require.Contains(t, options.Namespaces, metav1.NamespaceSystem)
	assert.True(t, options.Label.Matches(labels.Set(projected.Labels)))
}

func TestCiliumNodeProjection(t *testing.T) {
	t.Parallel()

	deletingAt := metav1.NewTime(time.Unix(30, 0))
	node := &ciliumv2.CiliumNode{
		ObjectMeta: fullObjectMeta("worker-a", "", &deletingAt),
		Spec: ciliumv2.CiliumNodeSpec{
			Addresses: []ciliumv2.NodeAddress{
				{Type: "CiliumInternalIP", IP: "10.1.0.1"},
				{Type: string(corev1.NodeInternalIP), IP: "192.0.2.10"},
			},
			Health: ciliumv2.HealthAddress{IPv4: "10.1.0.3"},
			IPAM:   ciliumv2.IPAMSpec{PodCIDRs: []string{"10.1.0.0/24"}},
		},
	}

	projected := applyTransform(t, node)

	assert.Equal(t, metav1.ObjectMeta{
		Name:            "worker-a",
		ResourceVersion: "42",
	}, projected.ObjectMeta)
	assert.Equal(t, []ciliumv2.NodeAddress{
		{Type: string(corev1.NodeInternalIP), IP: "192.0.2.10"},
	}, projected.Spec.Addresses)
	assert.Equal(t, "10.1.0.3", projected.Spec.Health.IPv4)
	assert.Equal(t, []string{"10.1.0.0/24"}, projected.Spec.IPAM.PodCIDRs)
}

func TestProjectionIgnoresUnexpectedObject(t *testing.T) {
	t.Parallel()

	unexpected := toolscache.DeletedFinalStateUnknown{Key: "worker-a", Obj: &corev1.Node{}}
	transform := optionsFor(t, &corev1.Node{}).Transform

	actual, err := transform(unexpected)

	require.NoError(t, err)
	assert.Equal(t, unexpected, actual)
}

func applyTransform[T client.Object](t *testing.T, object T) T {
	t.Helper()

	transformed, err := optionsFor(t, object).Transform(object)
	require.NoError(t, err)

	projected, ok := transformed.(T)
	require.True(t, ok)

	return projected
}

func optionsFor(t *testing.T, target client.Object) controllercache.ByObject {
	t.Helper()

	for object, options := range kubecache.Options().ByObject {
		if reflect.TypeOf(object) == reflect.TypeOf(target) {
			require.NotNil(t, options.Transform)

			return options
		}
	}

	require.FailNow(t, "cache options are missing", "object type: %T", target)

	return controllercache.ByObject{}
}

func fullObjectMeta(name, namespace string, deletingAt *metav1.Time) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:              name,
		Namespace:         namespace,
		UID:               types.UID("uid-1"),
		ResourceVersion:   "42",
		Generation:        7,
		DeletionTimestamp: deletingAt,
		Labels: map[string]string{
			kubecache.CiliumPodLabelKey: kubecache.CiliumPodLabelValue,
			"unused":                    "value",
		},
		Annotations:   map[string]string{"large": "unused"},
		ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "unused"}},
	}
}
