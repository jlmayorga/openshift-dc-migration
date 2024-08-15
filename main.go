package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"sigs.k8s.io/yaml"
)

var (
	kubeconfig            string
	outputDir             string
	applyChanges          bool
	preserveAnnotations   bool
	preserveLabels        bool
	reservedNamespaces    []string
	dcSpecificAnnotations []string
	dcSpecificLabels      []string
	logFilePath           string
	openShiftProjects     []string
	logFile               *os.File
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "openshift-dc-converter",
		Short: "Convert OpenShift DeploymentConfigs to Kubernetes Deployments",
		Long:  `A CLI tool to convert OpenShift DeploymentConfigs to Kubernetes Deployments across specified projects.`,
		RunE:  run,
	}

	rootCmd.Flags().StringVar(&kubeconfig, "kubeconfig", filepath.Join(homedir.HomeDir(), ".kube", "config"), "Path to the kubeconfig file")
	rootCmd.Flags().StringVar(&outputDir, "output-dir", "./converted_deployments", "Directory to store converted Deployment YAML files")
	rootCmd.Flags().BoolVar(&applyChanges, "apply-changes", false, "Apply the converted Deployments to the cluster")
	rootCmd.Flags().BoolVar(&preserveAnnotations, "preserve-annotations", true, "Preserve existing annotations in the converted Deployments")
	rootCmd.Flags().BoolVar(&preserveLabels, "preserve-labels", true, "Preserve existing labels in the converted Deployments")
	rootCmd.Flags().StringSliceVar(&reservedNamespaces, "reserved-namespaces", []string{"default", "openshift", "openshift-infra"}, "List of reserved namespaces to skip")
	rootCmd.Flags().StringVar(&logFilePath, "log-file", "conversion_log.txt", "Path to the log file")
	rootCmd.Flags().StringSliceVar(&openShiftProjects, "projects", []string{}, "List of OpenShift projects to scan and convert")

	if err := rootCmd.MarkFlagRequired("projects"); err != nil {
		fmt.Fprintf(os.Stderr, "Error marking 'projects' flag as required: %v\n", err)
		os.Exit(1)
	}

	dcSpecificAnnotations = []string{
		"openshift.io/deployment-config.name",
		"openshift.io/deployment-config.latest-version",
		"openshift.io/deployment.phase",
	}
	dcSpecificLabels = []string{
		"openshift.io/deployment-config.name",
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	var err error
	// Create log file
	logFile, err = os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("error creating log file: %v", err)
	}
	defer logFile.Close()

	// Initialize Kubernetes client
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return fmt.Errorf("error building kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("error creating Kubernetes client: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("error creating dynamic client: %v", err)
	}

	// Log start of migration
	logMessage(fmt.Sprintf("Migration started at %s", time.Now().Format(time.RFC3339)))

	// Validate input projects
	validProjects, err := validateProjects(clientset, openShiftProjects)
	if err != nil {
		return fmt.Errorf("error validating projects: %v", err)
	}

	// Process each valid project
	totalConversions := 0
	for _, project := range validProjects {
		logMessage(fmt.Sprintf("Processing project: %s", project))

		// Get DeploymentConfigs in the project
		dcList, err := getDCs(dynamicClient, project)
		if err != nil {
			logMessage(fmt.Sprintf("Error getting DeploymentConfigs in project %s: %v", project, err))
			continue
		}

		// Convert DeploymentConfigs to Deployments
		for _, dc := range dcList.Items {
			deployment, err := convertDCtoDeployment(&dc)
			if err != nil {
				logMessage(fmt.Sprintf("Error converting DeploymentConfig %s in project %s: %v", dc.GetName(), project, err))
				continue
			}

			// Save Deployment YAML
			err = saveDeploymentYAML(deployment, project)
			if err != nil {
				logMessage(fmt.Sprintf("Error saving Deployment YAML for %s in project %s: %v", deployment.GetName(), project, err))
				continue
			}

			totalConversions++

			// Apply Deployment if requested
			if applyChanges {
				err = applyDeployment(dynamicClient, deployment)
				if err != nil {
					logMessage(fmt.Sprintf("Error applying Deployment %s in project %s: %v", deployment.GetName(), project, err))
				}
			}
		}
	}

	// Log summary
	logMessage(fmt.Sprintf("Summary: Total projects processed: %d, Total DeploymentConfigs converted: %d", len(validProjects), totalConversions))

	return nil
}

func validateProjects(clientset *kubernetes.Clientset, projects []string) ([]string, error) {
	var validProjects []string
	for _, project := range projects {
		_, err := clientset.CoreV1().Namespaces().Get(context.Background(), project, metav1.GetOptions{})
		if err != nil {
			logMessage(fmt.Sprintf("Warning: Project %s not found or not accessible", project))
			continue
		}
		if isReservedNamespace(project) {
			logMessage(fmt.Sprintf("Warning: Project %s is a reserved namespace and will be skipped", project))
			continue
		}
		validProjects = append(validProjects, project)
	}
	return validProjects, nil
}

func isReservedNamespace(namespace string) bool {
	for _, reserved := range reservedNamespaces {
		if namespace == reserved || strings.HasPrefix(namespace, "openshift-") || strings.HasPrefix(namespace, "kube-") {
			return true
		}
	}
	return false
}

func getDCs(client dynamic.Interface, namespace string) (*unstructured.UnstructuredList, error) {
	dcRes := schema.GroupVersionResource{Group: "apps.openshift.io", Version: "v1", Resource: "deploymentconfigs"}
	return client.Resource(dcRes).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
}

func convertDCtoDeployment(dc *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	deployment := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
		},
	}

	// Copy metadata
	metadata, found, err := unstructured.NestedMap(dc.Object, "metadata")
	if err != nil {
		return nil, fmt.Errorf("error getting metadata: %v", err)
	}
	if !found {
		return nil, fmt.Errorf("metadata not found in DeploymentConfig")
	}

	// Handle labels
	if preserveLabels {
		labels, _, _ := unstructured.NestedStringMap(metadata, "labels")
		for _, label := range dcSpecificLabels {
			delete(labels, label)
		}
		if err := unstructured.SetNestedStringMap(metadata, labels, "labels"); err != nil {
			return nil, fmt.Errorf("error setting labels: %v", err)
		}
	} else {
		unstructured.RemoveNestedField(metadata, "labels")
	}

	// Handle annotations
	if preserveAnnotations {
		annotations, _, _ := unstructured.NestedStringMap(metadata, "annotations")
		for _, annotation := range dcSpecificAnnotations {
			delete(annotations, annotation)
		}
		annotations["openshift.io/generated-by"] = "deploymentconfig-to-deployment-migration"
		annotations["openshift.io/migration-timestamp"] = time.Now().Format(time.RFC3339)
		if err := unstructured.SetNestedStringMap(metadata, annotations, "annotations"); err != nil {
			return nil, fmt.Errorf("error setting annotations: %v", err)
		}
	} else {
		unstructured.RemoveNestedField(metadata, "annotations")
		metadata["annotations"] = map[string]interface{}{
			"openshift.io/generated-by":        "deploymentconfig-to-deployment-migration",
			"openshift.io/migration-timestamp": time.Now().Format(time.RFC3339),
		}
	}

	if err := unstructured.SetNestedMap(deployment.Object, metadata, "metadata"); err != nil {
		return nil, fmt.Errorf("error setting metadata: %v", err)
	}

	// Convert spec
	spec, found, err := unstructured.NestedMap(dc.Object, "spec")
	if err != nil {
		return nil, fmt.Errorf("error getting spec: %v", err)
	}
	if !found {
		return nil, fmt.Errorf("spec not found in DeploymentConfig")
	}

	// Set replicas
	replicas, found, err := unstructured.NestedInt64(spec, "replicas")
	if err != nil {
		return nil, fmt.Errorf("error getting replicas: %v", err)
	}
	if !found {
		replicas = 1 // Default to 1 if not specified
	}
	if err := unstructured.SetNestedField(deployment.Object, replicas, "spec", "replicas"); err != nil {
		return nil, fmt.Errorf("error setting replicas: %v", err)
	}

	// Set selector
	selector, found, err := unstructured.NestedMap(spec, "selector")
	if err != nil {
		return nil, fmt.Errorf("error getting selector: %v", err)
	}
	if !found {
		return nil, fmt.Errorf("selector not found in DeploymentConfig spec")
	}
	if err := unstructured.SetNestedMap(deployment.Object, map[string]interface{}{"matchLabels": selector}, "spec", "selector"); err != nil {
		return nil, fmt.Errorf("error setting selector: %v", err)
	}

	// Set template
	template, found, err := unstructured.NestedMap(spec, "template")
	if err != nil {
		return nil, fmt.Errorf("error getting template: %v", err)
	}
	if !found {
		return nil, fmt.Errorf("template not found in DeploymentConfig spec")
	}
	if err := unstructured.SetNestedMap(deployment.Object, template, "spec", "template"); err != nil {
		return nil, fmt.Errorf("error setting template: %v", err)
	}

	// Handle strategy
	strategy, found, err := unstructured.NestedMap(spec, "strategy")
	if err != nil {
		return nil, fmt.Errorf("error getting strategy: %v", err)
	}
	if found {
		deploymentStrategy := map[string]interface{}{}
		strategyType, _, _ := unstructured.NestedString(strategy, "type")
		switch strategyType {
		case "Rolling":
			deploymentStrategy["type"] = "RollingUpdate"
			rollingParams, _, _ := unstructured.NestedMap(strategy, "rollingParams")
			if rollingParams != nil {
				updateStrategy := map[string]interface{}{}
				if maxUnavailable, exists, _ := unstructured.NestedString(rollingParams, "maxUnavailable"); exists {
					updateStrategy["maxUnavailable"] = maxUnavailable
				}
				if maxSurge, exists, _ := unstructured.NestedString(rollingParams, "maxSurge"); exists {
					updateStrategy["maxSurge"] = maxSurge
				}
				deploymentStrategy["rollingUpdate"] = updateStrategy
			}
		case "Recreate":
			deploymentStrategy["type"] = "Recreate"
		default:
			deploymentStrategy["type"] = "RollingUpdate"
		}
		if err := unstructured.SetNestedMap(deployment.Object, deploymentStrategy, "spec", "strategy"); err != nil {
			return nil, fmt.Errorf("error setting strategy: %v", err)
		}
	}

	// Remove DeploymentConfig specific fields
	unstructured.RemoveNestedField(deployment.Object, "spec", "triggers")
	unstructured.RemoveNestedField(deployment.Object, "spec", "test")
	unstructured.RemoveNestedField(deployment.Object, "spec", "paused")

	return deployment, nil
}

func saveDeploymentYAML(deployment *unstructured.Unstructured, namespace string) error {
	data, err := yaml.Marshal(deployment)
	if err != nil {
		return err
	}

	dir := filepath.Join(outputDir, namespace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	filename := filepath.Join(dir, fmt.Sprintf("%s.yaml", deployment.GetName()))
	return os.WriteFile(filename, data, 0644)
}

func applyDeployment(client dynamic.Interface, deployment *unstructured.Unstructured) error {
	deploymentRes := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	_, err := client.Resource(deploymentRes).Namespace(deployment.GetNamespace()).Create(context.Background(), deployment, metav1.CreateOptions{})
	return err
}

func logMessage(message string) {
	fmt.Fprintln(logFile, message)
	fmt.Println(message)
}
