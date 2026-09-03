package controller

import (
	"context"

	ciliumv2 "github.com/zijiren233/route-controller/internal/apis/cilium/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerRuntime "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const controllerName = "routes"

var singletonRequest = reconcile.Request{NamespacedName: types.NamespacedName{Name: controllerName}}

func (reconciler *Reconciler) SetupWithManager(manager ctrl.Manager) error {
	enqueueSingleton := handler.EnqueueRequestsFromMapFunc(
		func(context.Context, client.Object) []reconcile.Request {
			return []reconcile.Request{singletonRequest}
		},
	)

	return ctrl.NewControllerManagedBy(manager).
		Named(controllerName).
		Watches(&corev1.Node{}, enqueueSingleton).
		Watches(&corev1.Pod{}, enqueueSingleton).
		Watches(&ciliumv2.CiliumNode{}, enqueueSingleton).
		WithOptions(controllerRuntime.Options{MaxConcurrentReconciles: 1}).
		Complete(reconciler)
}
