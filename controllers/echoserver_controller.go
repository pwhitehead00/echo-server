/*
Copyright 2022.

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

package controllers

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	serversv1alpha1 "pwhitehead00.io/echo-server/api/v1alpha1"
)

// EchoServerReconciler reconciles a EchoServer object
type EchoServerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=servers.pwhitehead00.io,resources=echoservers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=servers.pwhitehead00.io,resources=echoservers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=servers.pwhitehead00.io,resources=echoservers/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the EchoServer object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.1/pkg/reconcile
func (r *EchoServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	scheduledResult := ctrl.Result{RequeueAfter: 30 * time.Second}

	log := log.FromContext(ctx)

	var echoServer serversv1alpha1.EchoServer
	if err := r.Get(ctx, req.NamespacedName, &echoServer); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		log.Error(err, "unable to fetch EchoServer")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, err
	}

	// not working yet
	isMarkedToBeDeleted := echoServer.DeletionTimestamp != nil
	if isMarkedToBeDeleted {
		log.V(1).Info("Deleting EchoServer", "name", echoServer.Name, "namespace", echoServer.Namespace)
	}

	constructDeploy := func(echoServer *serversv1alpha1.EchoServer) (*appsv1.Deployment, error) {
		deploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      echoServer.Name,
				Namespace: echoServer.Namespace,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: echoServer.Spec.Replicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": "echoserver",
					},
				},
				Template: v1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"app": "echoserver",
						},
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Image: "gcr.io/google_containers/echoserver:1.0",
								Name:  "echoserver",
								Ports: []v1.ContainerPort{
									{
										ContainerPort: 8080,
										Name:          "http",
									},
								},
							},
						},
					},
				},
			},
		}

		if err := ctrl.SetControllerReference(echoServer, deploy, r.Scheme); err != nil {
			return nil, err
		}

		return deploy, nil
	}

	deploy, err := constructDeploy(&echoServer)
	if err != nil {
		log.Error(err, "unable to construct deployment")
		// don't bother requeuing until we get a change to the spec
		return ctrl.Result{}, nil
	}

	foundDeployment := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: deploy.Name, Namespace: deploy.Namespace}, foundDeployment)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("Creating Deployment", "deployment", deploy.Name)
		err = r.Create(ctx, deploy)
	} else if err == nil {
		if *foundDeployment.Spec.Replicas != *echoServer.Spec.Replicas {
			log.V(1).Info("Replicas dont match", "found:", foundDeployment.Spec.Replicas, "expected:", echoServer.Spec.Replicas)
			foundDeployment.Spec.Replicas = echoServer.Spec.Replicas
			log.V(1).Info("Updating Deployment", "deployment", echoServer.Name)
			r.Update(ctx, foundDeployment)
		}
	}

	// return ctrl.Result{}, nil
	return scheduledResult, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EchoServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serversv1alpha1.EchoServer{}).
		Owns(&appsv1.Deployment{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
