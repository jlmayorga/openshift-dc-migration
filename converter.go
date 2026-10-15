package main

import (
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"time"
)

func (r *Runner) convertDCtoDeployment(dc *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	deployment := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
		},
	}

	if err := r.copyMetadata(dc, deployment); err != nil {
		return nil, fmt.Errorf("failed to copy metadata: %w", err)
	}

	if err := convertSpec(dc, deployment); err != nil {
		return nil, fmt.Errorf("failed to convert spec: %w", err)
	}

	cleanupDeploymentConfig(deployment)

	return deployment, nil
}

func (r *Runner) copyMetadata(dc, deployment *unstructured.Unstructured) error {
	metadata, found, err := unstructured.NestedMap(dc.Object, "metadata")
	if err != nil {
		return fmt.Errorf("error getting metadata: %w", err)
	}
	if !found {
		return fmt.Errorf("metadata not found in DeploymentConfig")
	}

	newMetadata := make(map[string]interface{})
	newMetadata["name"] = metadata["name"]
	newMetadata["namespace"] = metadata["namespace"]

	if r.Options.PreserveLabels {
		if labels, ok := metadata["labels"].(map[string]interface{}); ok {
			newLabels := make(map[string]interface{})
			for k, v := range labels {
				if !contains(dcSpecificLabels, k) {
					newLabels[k] = v
				}
			}
			if len(newLabels) > 0 {
				newMetadata["labels"] = newLabels
			}
		}
	}

	newAnnotations := make(map[string]interface{})
	if r.Options.PreserveAnnotations {
		if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
			for k, v := range annotations {
				if !contains(dcSpecificAnnotations, k) {
					newAnnotations[k] = v
				}
			}
		}
	}

	newAnnotations["openshift.io/generated-by"] = "deploymentconfig-to-deployment-migration"
	newAnnotations["openshift.io/migration-timestamp"] = r.Now().Format(time.RFC3339)
	newMetadata["annotations"] = newAnnotations

	return unstructured.SetNestedMap(deployment.Object, newMetadata, "metadata")
}

func convertSpec(dc, deployment *unstructured.Unstructured) error {
	spec, found, err := unstructured.NestedMap(dc.Object, "spec")
	if err != nil {
		return fmt.Errorf("error getting spec: %w", err)
	}
	if !found {
		return fmt.Errorf("spec not found in DeploymentConfig")
	}

	if err := setReplicas(spec, deployment); err != nil {
		return fmt.Errorf("failed to set replicas: %w", err)
	}

	if err := setSelector(spec, deployment); err != nil {
		return fmt.Errorf("failed to set selector: %w", err)
	}

	if err := setTemplate(spec, deployment); err != nil {
		return fmt.Errorf("failed to set template: %w", err)
	}

	if err := setStrategy(spec, deployment); err != nil {
		return fmt.Errorf("failed to set strategy: %w", err)
	}

	return nil
}

func setReplicas(spec map[string]interface{}, deployment *unstructured.Unstructured) error {
	replicas, found, err := unstructured.NestedInt64(spec, "replicas")
	if err != nil {
		return fmt.Errorf("error getting replicas: %w", err)
	}
	if !found {
		replicas = 1 // Default to 1 if not specified
	}
	return unstructured.SetNestedField(deployment.Object, replicas, "spec", "replicas")
}

func setSelector(spec map[string]interface{}, deployment *unstructured.Unstructured) error {
	selector, found, err := unstructured.NestedMap(spec, "selector")
	if err != nil {
		return fmt.Errorf("error getting selector: %w", err)
	}
	if !found {
		return fmt.Errorf("selector not found in DeploymentConfig spec")
	}

	delete(selector, "deploymentconfig")

	return unstructured.SetNestedMap(deployment.Object, map[string]interface{}{"matchLabels": selector}, "spec", "selector")
}

func setTemplate(spec map[string]interface{}, deployment *unstructured.Unstructured) error {
	template, found, err := unstructured.NestedMap(spec, "template")
	if err != nil {
		return fmt.Errorf("error getting template: %w", err)
	}
	if !found {
		return fmt.Errorf("template not found in DeploymentConfig spec")
	}

	if templateMetadata, ok := template["metadata"].(map[string]interface{}); ok {
		if labels, ok := templateMetadata["labels"].(map[string]interface{}); ok {
			delete(labels, "deploymentconfig")
		}
	}

	return unstructured.SetNestedMap(deployment.Object, template, "spec", "template")
}

func setStrategy(spec map[string]interface{}, deployment *unstructured.Unstructured) error {
	strategy, found, err := unstructured.NestedMap(spec, "strategy")
	if err != nil {
		return fmt.Errorf("error getting strategy: %w", err)
	}
	if !found {
		return nil // No strategy to set
	}

	deploymentStrategy := map[string]interface{}{}
	strategyType, _, _ := unstructured.NestedString(strategy, "type")

	switch strategyType {
	case "Rolling":
		deploymentStrategy["type"] = "RollingUpdate"
		if rollingParams, ok := strategy["rollingParams"].(map[string]interface{}); ok {
			updateStrategy := map[string]interface{}{}
			if maxUnavailable, exists := rollingParams["maxUnavailable"]; exists {
				updateStrategy["maxUnavailable"] = maxUnavailable
			}
			if maxSurge, exists := rollingParams["maxSurge"]; exists {
				updateStrategy["maxSurge"] = maxSurge
			}
			deploymentStrategy["rollingUpdate"] = updateStrategy
		}
	case "Recreate":
		deploymentStrategy["type"] = "Recreate"
	default:
		deploymentStrategy["type"] = "RollingUpdate"
	}

	return unstructured.SetNestedMap(deployment.Object, deploymentStrategy, "spec", "strategy")
}

func cleanupDeploymentConfig(deployment *unstructured.Unstructured) {
	unstructured.RemoveNestedField(deployment.Object, "spec", "triggers")
	unstructured.RemoveNestedField(deployment.Object, "spec", "test")
	unstructured.RemoveNestedField(deployment.Object, "spec", "paused")
}
