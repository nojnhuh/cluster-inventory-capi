/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	inventoryv1 "github.com/Jont828/inventory-cluster/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/external"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ClusterAPIClusterInventoryReconciler reconciles a ClusterAPIClusterInventory object
type ClusterAPIClusterInventoryReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	externalTracker *external.ObjectTracker
}

//+kubebuilder:rbac:groups=cluster.x-k8s.io;controlplane.cluster.x-k8s.io,resources=*,verbs=get;list;watch
//+kubebuilder:rbac:groups=multicluster.x-k8s.io.multicluster.x-k8s.io,resources=inventoryclusters/status;inventoryclusters,verbs=get;list;watch;create;patch;delete

func (r *ClusterAPIClusterInventoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	cluster := &clusterv1.Cluster{}
	err := r.Get(ctx, req.NamespacedName, cluster)
	if err != nil {
		// standard garbage collection controls deletion through owner ref
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var controlPlaneVersion string
	if cluster.Spec.ControlPlaneRef != nil {
		controlPlane, err := external.Get(ctx, r.Client, cluster.Spec.ControlPlaneRef, cluster.Namespace)
		if err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		if err := r.externalTracker.Watch(log, controlPlane, handler.EnqueueRequestsFromMapFunc(toClusterFromLabel)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to watch control plane resource: %w", err)
		}
		controlPlaneVersion, _, err = unstructured.NestedString(controlPlane.UnstructuredContent(), "status", "version")
		if err != nil {
			return ctrl.Result{}, reconcile.TerminalError(err)
		}
	}

	ic := &inventoryv1.InventoryCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name,
			Namespace: cluster.Namespace,
		},
		Spec: inventoryv1.InventoryClusterSpec{
			DisplayName: cluster.Name,
			ClusterManager: inventoryv1.ClusterManager{
				Name: "cluster-api",
			},
		},
		Status: inventoryv1.InventoryClusterStatus{
			Version: controlPlaneVersion,
		},
	}
	ic.SetGroupVersionKind(inventoryv1.GroupVersion.WithKind("InventoryCluster"))

	if err := controllerutil.SetControllerReference(cluster, ic, r.Client.Scheme()); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("applying inventory cluster", "inventoryCluster", ic)
	err = r.Patch(ctx, ic.DeepCopy(), client.Apply, client.FieldOwner("capi-cluster-inventory"))
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.Status().Patch(ctx, ic, client.Apply, client.FieldOwner("capi-cluster-inventory"))
	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterAPIClusterInventoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1.Cluster{}).
		Owns(&inventoryv1.InventoryCluster{}).
		Build(r)
	if err != nil {
		return err
	}

	r.externalTracker = &external.ObjectTracker{
		Cache:      mgr.GetCache(),
		Controller: c,
	}

	return nil
}

func toClusterFromLabel(ctx context.Context, o client.Object) []reconcile.Request {
	clusterName := o.GetLabels()[clusterv1.ClusterNameLabel]
	if clusterName == "" {
		return nil
	}
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Namespace: o.GetNamespace(),
				Name:      clusterName,
			},
		},
	}
}
