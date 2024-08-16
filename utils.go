package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

func logMessage(message string) error {
	f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("error opening log file: %w", err)
	}
	defer f.Close()

	_, err = f.WriteString(message + "\n")
	return err
}

func validateProjects(client dynamic.Interface, projects []string) ([]string, error) {
	var validProjects []string
	ctx := context.Background()
	for _, project := range projects {

		if isReservedNamespace(project) {
			logMessage(fmt.Sprintf("Warning: Project %s is a reserved namespace and will be skipped", project))
			continue
		}

		_, err := client.Resource(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}).Get(ctx, project, metav1.GetOptions{})
		if err != nil {
			logMessage(fmt.Sprintf("Warning: Project %s not found or not accessible: %v", project, err))
			continue
		}
		validProjects = append(validProjects, project)
	}
	if len(validProjects) == 0 {
		return nil, fmt.Errorf("no valid projects found among the provided projects")
	}
	return validProjects, nil
}

func isReservedNamespace(namespace string) bool {
	if strings.HasPrefix(namespace, "openshift-") || strings.HasPrefix(namespace, "kube-") {
		return true
	}
	for _, reserved := range reservedNamespaces {
		if namespace == reserved {
			return true
		}
	}
	return false
}

func getDCs(client dynamic.Interface, namespace string) (*unstructured.UnstructuredList, error) {
	ctx := context.Background()
	dcRes := schema.GroupVersionResource{Group: "apps.openshift.io", Version: "v1", Resource: "deploymentconfigs"}
	return client.Resource(dcRes).Namespace(namespace).List(ctx, metav1.ListOptions{})
}

func saveDeploymentYAML(deployment *unstructured.Unstructured, namespace string) error {
	data, err := yaml.Marshal(deployment)
	if err != nil {
		return fmt.Errorf("error marshaling deployment to YAML: %w", err)
	}

	dir := filepath.Join(outputDir, namespace)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("error creating output directory: %w", err)
	}

	filename := filepath.Join(dir, fmt.Sprintf("%s.yaml", deployment.GetName()))
	return os.WriteFile(filename, data, 0644)
}

func applyDeployment(client dynamic.Interface, deployment *unstructured.Unstructured) error {
	ctx := context.Background()
	deploymentRes := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	_, err := client.Resource(deploymentRes).Namespace(deployment.GetNamespace()).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error applying deployment %s in namespace %s: %w", deployment.GetName(), deployment.GetNamespace(), err)
	}
	return nil
}

func hasTriggers(dc *unstructured.Unstructured) bool {
	triggers, _, _ := unstructured.NestedSlice(dc.Object, "spec", "triggers")
	return len(triggers) > 0
}

func hasLifecycleHooks(dc *unstructured.Unstructured) bool {
	_, preHookFound, _ := unstructured.NestedMap(dc.Object, "spec", "strategy", "recreateParams", "pre")
	_, midHookFound, _ := unstructured.NestedMap(dc.Object, "spec", "strategy", "recreateParams", "mid")
	_, postHookFound, _ := unstructured.NestedMap(dc.Object, "spec", "strategy", "recreateParams", "post")
	return preHookFound || midHookFound || postHookFound
}

func hasAutoRollbacks(dc *unstructured.Unstructured) bool {
	autoRollback, _, _ := unstructured.NestedBool(dc.Object, "spec", "strategy", "rollingParams", "autoRollbackEnabled")
	return autoRollback
}

func usesCustomStrategies(dc *unstructured.Unstructured) bool {
	strategyType, _, _ := unstructured.NestedString(dc.Object, "spec", "strategy", "type")
	return strategyType == "Custom"
}

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func preflightCheck(clientset *kubernetes.Clientset) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check if we can list namespaces
	_, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("failed to connect to the OpenShift cluster: %w", err)
	}

	// Check if we can access the OpenShift API
	_, err = clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("failed to access OpenShift API: %w", err)
	}

	// Check if we have necessary permissions
	_, err = clientset.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx, &authorizationv1.SelfSubjectRulesReview{
		Spec: authorizationv1.SelfSubjectRulesReviewSpec{
			Namespace: "default",
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to check permissions: %w", err)
	}

	return nil

}
