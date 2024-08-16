package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"k8s.io/client-go/util/homedir"
)

var (
	kubeconfig          string
	outputDir           string
	applyChanges        bool
	preserveAnnotations bool
	preserveLabels      bool
	reservedNamespaces  []string
	logFilePath         string
	openShiftProjects   []string
	reportPath          string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "openshift-dc-converter",
		Short: "Convert OpenShift DeploymentConfigs to Kubernetes Deployments",
		Long:  `A CLI tool to convert OpenShift DeploymentConfigs to Kubernetes Deployments across specified projects and generate a PDF report.`,
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
	rootCmd.Flags().StringVar(&reportPath, "report-path", "conversion_report.pdf", "Path to save the PDF report")

	if err := rootCmd.MarkFlagRequired("projects"); err != nil {
		fmt.Println("Error marking 'projects' flag as required:", err)
		os.Exit(1)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Println("Error executing command:", err)
		os.Exit(1)
	}
}
