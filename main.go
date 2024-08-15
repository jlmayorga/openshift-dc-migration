package main

import (
	"context"
	"fmt"
	"io"
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
	if err := run(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}

func run() error {
	rootCmd := &cobra.Command{
		Use:   "openshift-dc-converter",
		Short: "Convert OpenShift DeploymentConfigs to Kubernetes Deployments",
		Long:  `A CLI tool to convert OpenShift DeploymentConfigs to Kubernetes Deployments across specified projects.`,
		RunE:  runConverter,
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
		return fmt.Errorf("error marking 'projects' flag as required: %w", err)
	}

	dcSpecificAnnotations = []string{
		"openshift.io/deployment-config.name",
		"openshift.io/deployment-config.latest-version",
		"openshift.io/deployment.phase",
	}
	dcSpecificLabels = []string{
		"openshift.io/deployment-config.name",
	}

	return rootCmd.Execute()
}

func runConverter(cmd *cobra.Command, args []string) error {
	var err error
	// Create log file
	logFile, err = os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("error creating log file: %w", err)
	}
	defer func() {
		if closeErr := logFile.Close(); closeErr != nil {
			err = fmt.Errorf("error closing log file: %w", closeErr)
		}
	}()

	// Initialize Kubernetes client
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return fmt.Errorf("error building kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("error creating Kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("error creating dynamic client: %w", err)
	}

	// Log start of migration
	if err := logMessage(fmt.Sprintf("Migration started at %s", time.Now().Format(time.RFC3339))); err != nil {
		return fmt.Errorf("error logging migration start: %w", err)
	}

	// Validate input projects
	validProjects, err := validateProjects(clientset, openShiftProjects)
	if err != nil {
		return fmt.Errorf("error validating projects: %w", err)
	}

	// Process each valid project
	totalConversions := 0
	for _, project := range validProjects {
		if err := logMessage(fmt.Sprintf("Processing project: %s", project)); err != nil {
			return fmt.Errorf("error logging project processing: %w", err)
		}

		// Get DeploymentConfigs in the project
		dcList, err := getDCs(dynamicClient, project)
		if err != nil {
			if logErr := logMessage(fmt.Sprintf("Error getting DeploymentConfigs in project %s: %v", project, err)); logErr != nil {
				return fmt.Errorf("error logging DeploymentConfig retrieval failure: %w", logErr)
			}
			continue
		}

		// Convert DeploymentConfigs to Deployments
		for _, dc := range dcList.Items {
			deployment, err := convertDCtoDeployment(&dc)
			if err != nil {
				if logErr := logMessage(fmt.Sprintf("Error converting DeploymentConfig %s in project %s: %v", dc.GetName(), project, err)); logErr != nil {
					return fmt.Errorf("error logging DeploymentConfig conversion failure: %w", logErr)
				}
				continue
			}

			// Save Deployment YAML
			if err := saveDeploymentYAML(deployment, project); err != nil {
				if logErr := logMessage(fmt.Sprintf("Error saving Deployment YAML for %s in project %s: %v", deployment.GetName(), project, err)); logErr != nil {
					return fmt.Errorf("error logging Deployment YAML save failure: %w", logErr)
				}
				continue
			}

			totalConversions++

			// Apply Deployment if requested
			if applyChanges {
				if err := applyDeployment(dynamicClient, deployment); err != nil {
					if logErr := logMessage(fmt.Sprintf("Error applying Deployment %s in project %s: %v", deployment.GetName(), project, err)); logErr != nil {
						return fmt.Errorf("error logging Deployment application failure: %w", logErr)
					}
				}
			}
		}
	}

	// Log summary
	if err := logMessage(fmt.Sprintf("Summary: Total projects processed: %d, Total DeploymentConfigs converted: %d", len(validProjects), totalConversions)); err != nil {
		return fmt.Errorf("error logging summary: %w", err)
	}

	return nil
}

func validateProjects(clientset *kubernetes.Clientset, projects []string) ([]string, error) {
	var validProjects []string
	for _, project := range projects {
		_, err := clientset.CoreV1().Namespaces().Get(context.Background(), project, metav1.GetOptions{})
		if err != nil {
			if logErr := logMessage(fmt.Sprintf("Warning: Project %s not found or not accessible: %v", project, err)); logErr != nil {
				return nil, fmt.Errorf("error logging project validation warning: %w", logErr)
			}
			continue
		}
		if isReservedNamespace(project) {
			if logErr := logMessage(fmt.Sprintf("Warning: Project %s is a reserved namespace and will be skipped", project)); logErr != nil {
				return nil, fmt.Errorf("error logging reserved namespace warning: %w", logErr)
			}
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
		return nil, fmt.Errorf("error getting metadata: %w", err)
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
			return nil, fmt.Errorf("error setting labels: %w", err)
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
			return nil, fmt.Errorf("error setting annotations: %w", err)
		}
	} else {
		unstructured.RemoveNestedField(metadata, "annotations")
		metadata["annotations"] = map[string]interface{}{
			"openshift.io/generated-by":        "deploymentconfig-to-deployment-migration",
			"openshift.io/migration-timestamp": time.Now().Format(time.RFC3339),
		}
	}

	if err := unstructured.SetNestedMap(deployment.Object, metadata, "metadata"); err != nil {
		return nil, fmt.Errorf("error setting metadata: %w", err)
	}

	// Convert spec
	spec, found, err := unstructured.NestedMap(dc.Object, "spec")
	if err != nil {
		return nil, fmt.Errorf("error getting spec: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("spec not found in DeploymentConfig")
	}

	// Set replicas
	replicas, found, err := unstructured.NestedInt64(spec, "replicas")
	if err != nil {
		return nil, fmt.Errorf("error getting replicas: %w", err)
	}
	if !found {
		replicas = 1 // Default to 1 if not specified
	}
	if err := unstructured.SetNestedField(deployment.Object, replicas, "spec", "replicas"); err != nil {
		return nil, fmt.Errorf("error setting replicas: %w", err)
	}

	// Set selector
	selector, found, err := unstructured.NestedMap(spec, "selector")
	if err != nil {
		return nil, fmt.Errorf("error getting selector: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("selector not found in DeploymentConfig spec")
	}
	if err := unstructured.SetNestedMap(deployment.Object, map[string]interface{}{"matchLabels": selector}, "spec", "selector"); err != nil {
		return nil, fmt.Errorf("error setting selector: %w", err)
	}

	// Set template
	template, found, err := unstructured.NestedMap(spec, "template")
	if err != nil {
		return nil, fmt.Errorf("error getting template: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("template not found in DeploymentConfig spec")
	}
	if err := unstructured.SetNestedMap(deployment.Object, template, "spec", "template"); err != nil {
		return nil, fmt.Errorf("error setting template: %w", err)
	}

	// Handle strategy
	strategy, found, err := unstructured.NestedMap(spec, "strategy")
	if err != nil {
		return nil, fmt.Errorf("error getting strategy: %w", err)
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
			return nil, fmt.Errorf("error setting strategy: %w", err)
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
		return fmt.Errorf("error marshaling deployment to YAML: %w", err)
	}

	dir := filepath.Join(outputDir, namespace)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("error creating output directory: %w", err)
	}

	filename := filepath.Join(dir, fmt.Sprintf("%s.yaml", deployment.GetName()))
	tempFile, err := os.CreateTemp(dir, "temp-deployment-*.yaml")
	if err != nil {
		return fmt.Errorf("error creating temporary file: %w", err)
	}
	defer func() {
		tempFile.Close()
		os.Remove(tempFile.Name())
	}()

	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("error writing to temporary file: %w", err)
	}

	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("error syncing temporary file: %w", err)
	}

	if err := os.Rename(tempFile.Name(), filename); err != nil {
		return fmt.Errorf("error renaming temporary file: %w", err)
	}

	return nil
}

func applyDeployment(client dynamic.Interface, deployment *unstructured.Unstructured) error {
	deploymentRes := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	_, err := client.Resource(deploymentRes).Namespace(deployment.GetNamespace()).Create(context.Background(), deployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error applying deployment %s in namespace %s: %w", deployment.GetName(), deployment.GetNamespace(), err)
	}
	return nil
}

func logMessage(message string) error {
	timestamp := time.Now().Format(time.RFC3339)
	logEntry := fmt.Sprintf("[%s] %s\n", timestamp, message)

	if _, err := io.WriteString(logFile, logEntry); err != nil {
		return fmt.Errorf("error writing to log file: %w", err)
	}

	_, err := fmt.Print(logEntry)
	return err
}
