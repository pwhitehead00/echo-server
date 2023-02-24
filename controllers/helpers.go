package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	serversv1alpha1 "pwhitehead00.io/echo-server/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *EchoServerReconciler) creater(ctx context.Context, echoServer serversv1alpha1.EchoServer, obj client.Object, log logr.Logger) error {
	log.V(1).Info("Creating blah", "blah", obj.GetName())
	if err := r.Create(ctx, obj); err != nil {
		echoServer.Status.State = Failed
		if err := r.Status().Update(ctx, &echoServer); err != nil {
			r.Recorder.Event(&echoServer, v1.EventTypeWarning, Failed, fmt.Sprint("Failed to set status"))
			return err
		}
		r.Recorder.Event(&echoServer, v1.EventTypeWarning, Failed, fmt.Sprintf("Failed to create %v", obj.GetName()))
		return err
	}

	r.Recorder.Event(&echoServer, v1.EventTypeNormal, Created, fmt.Sprintf("Created %v %v", obj.GetObjectKind().GroupVersionKind().Version, obj.GetName()))
	return nil
}
