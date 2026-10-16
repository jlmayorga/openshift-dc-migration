package main

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var hpaResource = schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}
var replicaSetResource = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}

const deploymentSpecSchema = "io.k8s.api.apps.v1.DeploymentSpec"
const hpaSpecSchema = "io.k8s.api.autoscaling.v2.HorizontalPodAutoscalerSpec"
const replicaSetSpecSchema = "io.k8s.api.apps.v1.ReplicaSetSpec"

func validateResourceSpec(object *unstructured.Unstructured, root string) error {
	spec, found, err := unstructured.NestedMap(object.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("%s %s omitted a valid spec", object.GetKind(), object.GetName())
	}
	if err = validateBaseline(spec, root); err != nil {
		return fmt.Errorf("%s %s raw spec needs manual review: %w", object.GetKind(), object.GetName(), err)
	}
	return nil
}

// An empty root permits metadata/known-field inspection for explicit recovery.
// Such objects must never be round-tripped through this typed projection.
func decodeResource[T any](object *unstructured.Unstructured, root string) (*T, error) {
	if root != "" {
		if err := validateResourceSpec(object, root); err != nil {
			return nil, err
		}
	}
	var result T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func resourceObject(object interface{}, resource schema.GroupVersionResource, kind string) (*unstructured.Unstructured, error) {
	var result *unstructured.Unstructured
	if raw, ok := object.(*unstructured.Unstructured); ok {
		result = raw.DeepCopy()
	} else {
		data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
		if err != nil {
			return nil, err
		}
		result = &unstructured.Unstructured{Object: data}
	}
	result.SetAPIVersion(resource.GroupVersion().String())
	result.SetKind(kind)
	return result, nil
}

func (r *Runner) createResource(ctx context.Context, resource schema.GroupVersionResource, kind string, object interface{}, options metav1.CreateOptions) (*unstructured.Unstructured, error) {
	request, err := resourceObject(object, resource, kind)
	if err != nil {
		return nil, err
	}
	return r.Dynamic.Resource(resource).Namespace(request.GetNamespace()).Create(ctx, request, options)
}

func (r *Runner) updateResource(ctx context.Context, resource schema.GroupVersionResource, kind string, object interface{}, options metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	request, err := resourceObject(object, resource, kind)
	if err != nil {
		return nil, err
	}
	return r.Dynamic.Resource(resource).Namespace(request.GetNamespace()).Update(ctx, request, options)
}

func (r *Runner) getDeploymentState(ctx context.Context, ns, name string, strict bool) (*unstructured.Unstructured, *appsv1.Deployment, error) {
	object, err := r.Dynamic.Resource(deploymentResource).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	root := ""
	if strict {
		root = deploymentSpecSchema
	}
	decoded, err := decodeResource[appsv1.Deployment](object, root)
	return object, decoded, err
}

func (r *Runner) getDeployment(ctx context.Context, ns, name string) (*appsv1.Deployment, error) {
	_, decoded, err := r.getDeploymentState(ctx, ns, name, true)
	return decoded, err
}

func (r *Runner) createDeployment(ctx context.Context, object *appsv1.Deployment, options metav1.CreateOptions) (*appsv1.Deployment, error) {
	response, err := r.createResource(ctx, deploymentResource, "Deployment", object, options)
	if err != nil {
		return nil, err
	}
	return decodeResource[appsv1.Deployment](response, deploymentSpecSchema)
}

func (r *Runner) updateDeployment(ctx context.Context, object *appsv1.Deployment, options metav1.UpdateOptions) (*appsv1.Deployment, error) {
	response, err := r.updateResource(ctx, deploymentResource, "Deployment", object, options)
	if err != nil {
		return nil, err
	}
	return decodeResource[appsv1.Deployment](response, deploymentSpecSchema)
}

func (r *Runner) getHPA(ctx context.Context, ns, name string) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	object, err := r.Dynamic.Resource(hpaResource).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return decodeResource[autoscalingv2.HorizontalPodAutoscaler](object, hpaSpecSchema)
}

func (r *Runner) createHPAObject(ctx context.Context, object *autoscalingv2.HorizontalPodAutoscaler, options metav1.CreateOptions) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	response, err := r.createResource(ctx, hpaResource, "HorizontalPodAutoscaler", object, options)
	if err != nil {
		return nil, err
	}
	return decodeResource[autoscalingv2.HorizontalPodAutoscaler](response, hpaSpecSchema)
}

func (r *Runner) dryRunReplicaSet(ctx context.Context, object *appsv1.ReplicaSet) (*appsv1.ReplicaSet, error) {
	response, err := r.createResource(ctx, replicaSetResource, "ReplicaSet", object, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
	if err != nil {
		return nil, err
	}
	return decodeResource[appsv1.ReplicaSet](response, replicaSetSpecSchema)
}
