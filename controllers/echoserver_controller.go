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
	"fmt"
	"reflect"
	"strconv"
	"time"

	hashstructure "github.com/mitchellh/hashstructure/v2"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	serversv1alpha1 "pwhitehead00.io/echo-server/api/v1alpha1"
)

// EchoServerReconciler reconciles a EchoServer object
type EchoServerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// Common event reasons
const (
	Created string = "Created"
	Failed  string = "Failed"
)

// ConfigMap event reason list
const (
	ReconcileConfigMapData       string = "ReconcileConfigMapData"
	FailedReconcileConfigMapData string = "FailedReconcileConfigMapData"
)

//+kubebuilder:rbac:groups=servers.pwhitehead00.io,resources=echoservers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=servers.pwhitehead00.io,resources=echoservers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=servers.pwhitehead00.io,resources=echoservers/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

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

	configMap, err := r.newConfigMap(&echoServer)
	if err != nil {
		log.Error(err, "unable to construct configMap")
		return ctrl.Result{}, nil
	}

	foundConfigMap := &v1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace}, foundConfigMap)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("Creating ConfigMap", "configmap", configMap.Name)
		err = r.Create(ctx, configMap)
		if err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(&echoServer, v1.EventTypeNormal, "Created", fmt.Sprintf("Created configMap %s/%s", configMap.Namespace, configMap.Name))
	} else if err == nil {
		// reconcile configMap.Data
		if !reflect.DeepEqual(foundConfigMap.Data, configMap.Data) {
			log.V(1).Info("configMap out of sync", "found:", foundConfigMap.Data, "expected:", configMap.Data)
			foundConfigMap.Data = configMap.Data
			err := r.Update(ctx, foundConfigMap)
			if err != nil {
				log.V(1).Error(err, "Failed updating ConfigMap", "configmap", echoServer.Name)
				r.Recorder.Event(&echoServer, v1.EventTypeWarning, "FailedReconcileConfigMapData", fmt.Sprintf("Failed to reconciled configMap Data %s/%s", configMap.Namespace, configMap.Name))
				return ctrl.Result{}, err
			}
			r.Recorder.Event(&echoServer, v1.EventTypeWarning, ReconcileConfigMapData, fmt.Sprintf("Reconciled configMap Data %s/%s", configMap.Namespace, configMap.Name))
		}
	}

	// only hash the configMap.Data map to avoid unnecessary reconciliation during creation
	hash, err := hashstructure.Hash(configMap.Data, hashstructure.FormatV2, nil)
	if err != nil {
		log.V(1).Error(err, "Failed to hash configMap.Data", "configmap", echoServer.Name)
		r.Recorder.Event(&echoServer, v1.EventTypeNormal, Failed, fmt.Sprintf("Error: Failed to hash configMap data %s/%s", configMap.Namespace, configMap.Name))
		return ctrl.Result{}, err
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
						"app": "caddy",
					},
				},
				Template: v1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"app":  "caddy",
							"hash": strconv.FormatUint(hash, 10),
						},
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Image: "caddy:2.6.2",
								Name:  "caddy",
								Ports: []v1.ContainerPort{
									{
										ContainerPort: 80,
										Name:          "http",
									},
								},
								VolumeMounts: []v1.VolumeMount{
									{
										Name:      "config-volume",
										MountPath: "/usr/share/caddy",
									},
								},
							},
						},
						Volumes: []v1.Volume{
							{
								Name: "config-volume",
								VolumeSource: v1.VolumeSource{
									ConfigMap: &v1.ConfigMapVolumeSource{
										LocalObjectReference: v1.LocalObjectReference{
											Name: echoServer.Name,
										},
										Items: []v1.KeyToPath{
											{
												Key:  "index.html",
												Path: "index.html",
											},
										},
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
		if err != nil {
			log.V(1).Info("Failed to create Deployment", "error", err)
		}
	} else if err == nil {
		// reconcile deployment replicas
		if *foundDeployment.Spec.Replicas != *echoServer.Spec.Replicas {
			log.V(1).Info("Replicas out of sync", "found:", foundDeployment.Spec.Replicas, "expected:", echoServer.Spec.Replicas)
			foundDeployment.Spec.Replicas = echoServer.Spec.Replicas
			err := r.Update(ctx, foundDeployment)
			if err != nil {
				log.V(1).Error(err, "Failed to update deployment", "deployment", echoServer.Name)
				return ctrl.Result{}, err
			}
		}

		// reconcile deployment labels.  this will force pods to roll if the configmap changes
		if !reflect.DeepEqual(foundDeployment.Spec.Template.ObjectMeta.Labels, deploy.Spec.Template.ObjectMeta.Labels) {
			log.V(1).Info("Pod template out of sync", "found:", foundDeployment.Spec.Template.ObjectMeta.Labels, "expected:", deploy.Spec.Template.ObjectMeta.Labels)
			foundDeployment.Spec.Template.ObjectMeta.Labels = deploy.Spec.Template.ObjectMeta.Labels
			err := r.Update(ctx, foundDeployment)
			if err != nil {
				log.V(1).Error(err, "Failed to reconcile labels", "deployment", echoServer.Name)
				return ctrl.Result{}, err
			}
		}
	}

	service, err := r.newService(&echoServer)
	if err != nil {
		log.Error(err, "unable to construct service")
		return ctrl.Result{}, nil
	}

	foundService := &v1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, foundService)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("Creating Service", "service", service.Name)
		err = r.Create(ctx, service)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else if err == nil {
		// reconcile service ports
		if !reflect.DeepEqual(foundService.Spec.Ports, service.Spec.Ports) {
			log.V(1).Info("Service ports out of sync", "found:", foundService.Spec.Ports, "expected:", service.Spec.Ports)
			foundService.Spec.Ports[0] = service.Spec.Ports[0]
			err := r.Update(ctx, foundService)
			if err != nil {
				log.V(1).Error(err, "Failed to reconcie service ports", "name", echoServer.Name, "namespace", echoServer.Namespace)
				return ctrl.Result{}, err
			}
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
		Owns(&v1.Service{}).
		Owns(&v1.ConfigMap{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}

func (r *EchoServerReconciler) newService(echoServer *serversv1alpha1.EchoServer) (*v1.Service, error) {
	service := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      echoServer.Name,
			Namespace: echoServer.Namespace,
		},
		Spec: v1.ServiceSpec{
			Type:     v1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": "caddy"},
			Ports: []v1.ServicePort{
				{
					Name:       "http",
					Protocol:   v1.ProtocolTCP,
					Port:       echoServer.Spec.Port,
					TargetPort: intstr.FromInt(80),
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(echoServer, service, r.Scheme); err != nil {
		return nil, err
	}

	return service, nil
}

func (r *EchoServerReconciler) newConfigMap(echoServer *serversv1alpha1.EchoServer) (*v1.ConfigMap, error) {
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      echoServer.Name,
			Namespace: echoServer.Namespace,
		},
		Data: map[string]string{
			"index.html": echoServer.Spec.Text,
		},
	}

	if err := ctrl.SetControllerReference(echoServer, configMap, r.Scheme); err != nil {
		return nil, err
	}

	return configMap, nil
}
