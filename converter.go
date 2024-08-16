package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func runConverter(cmd *cobra.Command, args []string) error {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return fmt.Errorf("error building kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("error creating Kubernetes clientset: %w", err)
	}

	// Perform preflight check
	if err := preflightCheck(clientset); err != nil {
		return fmt.Errorf("preflight check failed: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("error creating dynamic client: %w", err)
	}

	validProjects, err := validateProjects(dynamicClient, openShiftProjects)
	if err != nil {
		return fmt.Errorf("error validating projects: %w", err)
	}

	for _, project := range validProjects {
		if err := processProject(dynamicClient, project); err != nil {
			return fmt.Errorf("error processing project %s: %w", project, err)
		}
	}

	if err := generatePDFReport(reportPath); err != nil {
		return fmt.Errorf("error generating PDF report: %w", err)
	}

	return nil
}

func processProject(client dynamic.Interface, namespace string) error {
	defer func() {
		if r := recover(); r != nil {
			err := logMessage(fmt.Sprintf("Panic occurred while processing project %s: %v", namespace, r))
			if err != nil {
				fmt.Printf("Failed to log message: %v\n", err)
			}
		}
	}()

	dcList, err := getDCs(client, namespace)
	if err != nil {
		return fmt.Errorf("error getting DeploymentConfigs in project %s: %w", namespace, err)
	}

	for _, dc := range dcList.Items {
		func() {
			defer func() {
				if r := recover(); r != nil {
					err := logMessage(fmt.Sprintf("Panic occurred while processing DeploymentConfig %s in project %s: %v", dc.GetName(), namespace, r))
					if err != nil {
						fmt.Printf("Failed to log message: %v\n", err)
					}
				}
			}()

			conversionInfo := ConversionInfo{
				Timestamp:            time.Now().Format(time.RFC3339),
				Namespace:            namespace,
				DeploymentConfigName: dc.GetName(),
				HasTriggers:          hasTriggers(&dc),
				HasLifecycleHooks:    hasLifecycleHooks(&dc),
				HasAutoRollbacks:     hasAutoRollbacks(&dc),
				UsesCustomStrategies: usesCustomStrategies(&dc),
			}

			deployment, err := convertDCtoDeployment(&dc)
			if err != nil {
				logErr := logMessage(fmt.Sprintf("Error converting DeploymentConfig %s in project %s: %v", dc.GetName(), namespace, err))
				if logErr != nil {
					fmt.Printf("Failed to log message: %v\n", logErr)
				}
				return
			}

			if err := saveDeploymentYAML(deployment, namespace); err != nil {
				logErr := logMessage(fmt.Sprintf("Error saving Deployment YAML for %s in project %s: %v", deployment.GetName(), namespace, err))
				if logErr != nil {
					fmt.Printf("Failed to log message: %v\n", logErr)
				}
				return
			}

			if applyChanges {
				if err := applyDeployment(client, deployment); err != nil {
					logErr := logMessage(fmt.Sprintf("Error applying Deployment %s in project %s: %v", deployment.GetName(), namespace, err))
					if logErr != nil {
						fmt.Printf("Failed to log message: %v\n", logErr)
					}
				}
			}

			conversionInfos = append(conversionInfos, conversionInfo)
		}()
	}

	return nil
}

func convertDCtoDeployment(dc *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	deployment := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
		},
	}

	if err := copyMetadata(dc, deployment); err != nil {
		return nil, fmt.Errorf("failed to copy metadata: %w", err)
	}

	if err := convertSpec(dc, deployment); err != nil {
		return nil, fmt.Errorf("failed to convert spec: %w", err)
	}

	cleanupDeploymentConfig(deployment)

	return deployment, nil
}

func copyMetadata(dc, deployment *unstructured.Unstructured) error {
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

	if preserveLabels {
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
	if preserveAnnotations {
		if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
			for k, v := range annotations {
				if !contains(dcSpecificAnnotations, k) {
					newAnnotations[k] = v
				}
			}
		}
	}

	newAnnotations["openshift.io/generated-by"] = "deploymentconfig-to-deployment-migration"
	newAnnotations["openshift.io/migration-timestamp"] = time.Now().Format(time.RFC3339)
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
