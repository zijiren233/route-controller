package kubecache

import (
	ciliumv2 "github.com/zijiren233/route-controller/internal/apis/cilium/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// RouteChanges compares objects projected by Options. Resource versions still
// advance in the cache, but updates to discarded fields do not trigger routing.
// Periodic reconciliation uses RequeueAfter, independently of informer resync.
func RouteChanges() predicate.Predicate {
	return predicate.Funcs{UpdateFunc: func(update event.UpdateEvent) bool {
		switch old := update.ObjectOld.(type) {
		case *corev1.Node:
			current, ok := update.ObjectNew.(*corev1.Node)
			return !ok || !old.DeletionTimestamp.Equal(current.DeletionTimestamp) ||
				!equality.Semantic.DeepEqual(old.Status, current.Status)
		case *corev1.Pod:
			current, ok := update.ObjectNew.(*corev1.Pod)
			return !ok || !old.DeletionTimestamp.Equal(current.DeletionTimestamp) ||
				!equality.Semantic.DeepEqual(old.Spec, current.Spec) ||
				!equality.Semantic.DeepEqual(old.Status, current.Status)
		case *ciliumv2.CiliumNode:
			current, ok := update.ObjectNew.(*ciliumv2.CiliumNode)
			return !ok || !equality.Semantic.DeepEqual(old.Spec, current.Spec)
		default:
			return true
		}
	}}
}
