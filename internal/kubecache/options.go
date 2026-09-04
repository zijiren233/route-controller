// Package kubecache configures the minimal Kubernetes objects retained by informers.
package kubecache

import (
	ciliumv2 "github.com/zijiren233/route-controller/internal/apis/cilium/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CiliumPodLabelKey   = "k8s-app"
	CiliumPodLabelValue = "cilium"
)

// Options limits both the objects watched and the fields retained in memory.
func Options() cache.Options {
	return cache.Options{
		ReaderFailOnMissingInformer: true,
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Node{}: {
				Transform: transform(projectNode),
			},
			&corev1.Pod{}: {
				Namespaces: map[string]cache.Config{metav1.NamespaceSystem: {}},
				Label: labels.SelectorFromSet(labels.Set{
					CiliumPodLabelKey: CiliumPodLabelValue,
				}),
				Transform: transform(projectPod),
			},
			&ciliumv2.CiliumNode{}: {
				Transform: transform(projectCiliumNode),
			},
		},
	}
}

func transform[T client.Object](project func(T)) toolscache.TransformFunc {
	return func(object any) (any, error) {
		value, ok := object.(T)
		if !ok {
			return object, nil
		}

		project(value)

		return value, nil
	}
}

func projectNode(node *corev1.Node) {
	node.ObjectMeta = metav1.ObjectMeta{
		Name:              node.Name,
		ResourceVersion:   node.ResourceVersion,
		DeletionTimestamp: node.DeletionTimestamp,
	}
	node.Spec = corev1.NodeSpec{}
	node.Status = corev1.NodeStatus{
		Addresses:  internalNodeAddresses(node.Status.Addresses),
		Conditions: nodeReadyCondition(node.Status.Conditions),
	}
}

func projectPod(pod *corev1.Pod) {
	pod.ObjectMeta = metav1.ObjectMeta{
		Name:              pod.Name,
		Namespace:         pod.Namespace,
		ResourceVersion:   pod.ResourceVersion,
		DeletionTimestamp: pod.DeletionTimestamp,
		Labels: map[string]string{
			CiliumPodLabelKey: pod.Labels[CiliumPodLabelKey],
		},
	}
	pod.Spec = corev1.PodSpec{NodeName: pod.Spec.NodeName}
	pod.Status = corev1.PodStatus{
		Phase:      pod.Status.Phase,
		Conditions: podReadyCondition(pod.Status.Conditions),
	}
}

func projectCiliumNode(node *ciliumv2.CiliumNode) {
	node.ObjectMeta = metav1.ObjectMeta{
		Name:            node.Name,
		ResourceVersion: node.ResourceVersion,
	}
	node.Spec.Addresses = ciliumInternalNodeAddresses(node.Spec.Addresses)
}

func internalNodeAddresses(source []corev1.NodeAddress) []corev1.NodeAddress {
	projected := make([]corev1.NodeAddress, 0, 1)
	for _, address := range source {
		if address.Type == corev1.NodeInternalIP {
			projected = append(projected, address)
		}
	}

	return projected
}

func nodeReadyCondition(source []corev1.NodeCondition) []corev1.NodeCondition {
	for _, condition := range source {
		if condition.Type == corev1.NodeReady {
			return []corev1.NodeCondition{{Type: condition.Type, Status: condition.Status}}
		}
	}

	return nil
}

func podReadyCondition(source []corev1.PodCondition) []corev1.PodCondition {
	for _, condition := range source {
		if condition.Type == corev1.PodReady {
			return []corev1.PodCondition{{Type: condition.Type, Status: condition.Status}}
		}
	}

	return nil
}

func ciliumInternalNodeAddresses(source []ciliumv2.NodeAddress) []ciliumv2.NodeAddress {
	projected := make([]ciliumv2.NodeAddress, 0, 1)
	for _, address := range source {
		if address.Type == string(corev1.NodeInternalIP) {
			projected = append(projected, address)
		}
	}

	return projected
}
