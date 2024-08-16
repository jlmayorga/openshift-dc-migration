# OpenShift DeploymentConfig to Deployment Converter

## Overview

This tool automates the process of converting OpenShift DeploymentConfigs to Kubernetes Deployments. It addresses the critical need to migrate workloads from the deprecated DeploymentConfig resource to the standard Kubernetes Deployment resource.

DeploymentConfigs have been deprecated since OpenShift 4.14. While they continue to function in current versions, they could be removed in a future release of OpenShift. This tool facilitates a smooth and efficient transition by automating the conversion process.

## Features

- Automatically identifies and skips reserved OpenShift namespaces
- Converts DeploymentConfigs to Deployments across multiple specified projects
- Generates YAML files for the new Deployments
- Optional application of the generated Deployments to the cluster
- Adds annotations to track the migration process
- Preserves existing labels and annotations (configurable)
- Generates a comprehensive PDF report of the conversion process
- Performs preflight checks to ensure cluster connectivity and permissions

## Prerequisites

- Go 1.21 or higher
- Access to an OpenShift cluster (via kubeconfig)
- Proper permissions in the OpenShift cluster to read DeploymentConfigs and create Deployments

## Installation

### From Source

1. Clone the repository:
   ```
   git clone https://github.com/jlmayorga/openshift-dc-migration.git
   cd openshift-dc-migration
   ```

2. Build the binary:
   ```
   go build -o openshift-dc-migration
   ```

### Using Pre-built Binaries

You can download pre-built binaries for various platforms from the [Releases](https://github.com/jlmayorga/openshift-dc-migration/releases) page.

## Usage

```
./openshift-dc-migration [flags]
```

### Flags

- `--kubeconfig`: Path to the kubeconfig file (default is `$HOME/.kube/config`)
- `--output-dir`: Directory to store converted Deployment YAML files (default is `./converted_deployments`)
- `--apply-changes`: Apply the converted Deployments to the cluster (default is false)
- `--preserve-annotations`: Preserve existing annotations in the converted Deployments (default is true)
- `--preserve-labels`: Preserve existing labels in the converted Deployments (default is true)
- `--reserved-namespaces`: List of reserved namespaces to skip (default is "default,openshift,openshift-infra")
- `--log-file`: Path to the log file (default is "conversion_log.txt")
- `--projects`: List of OpenShift projects to scan and convert (required)
- `--report-path`: Path to save the PDF report (default is "conversion_report.pdf")

### Example

To convert DeploymentConfigs in projects "project1" and "project2" without applying changes and generate a PDF report:

```
./openshift-dc-migration --projects=project1,project2 --output-dir=./converted --report-path=./migration_report.pdf
```

To convert, apply changes, and generate a PDF report:

```
./openshift-dc-migration --projects=project1,project2 --apply-changes=true --report-path=./migration_report.pdf
```

## Output

The tool will create a directory structure as follows:

```
./converted_deployments/
  ├── project1/
  │   ├── deployment1.yaml
  │   └── deployment2.yaml
  └── project2/
      ├── deployment3.yaml
      └── deployment4.yaml
```

Each generated Deployment YAML file will include annotations indicating it was created by this migration process and the timestamp of creation.

## PDF Report

The tool generates a comprehensive PDF report of the conversion process. This report includes:

- A list of all processed DeploymentConfigs
- The namespace and name of each DeploymentConfig
- Information about triggers, lifecycle hooks, auto-rollbacks, and custom strategies for each DeploymentConfig
- Conversion status and any errors encountered
- A summary of the total number of conversions performed

## Preflight Checks

Before performing any conversions, the tool now conducts preflight checks to ensure:

- Connectivity to the OpenShift cluster
- Proper permissions to access and modify necessary resources
- Validity of specified projects

## Warnings and Considerations

- Always run the tool without the `--apply-changes` flag first and review the generated YAML files before applying changes.
- Ensure you have backups of your DeploymentConfigs before running this tool with `--apply-changes=true`.
- This tool performs a basic conversion. You may need to manually adjust the generated Deployments for workloads with complex configurations.
- Test thoroughly in a non-production environment before using in production.
- Review the generated PDF report to ensure all conversions were successful and to document the migration process.

## Contributing

Contributions to improve the tool are welcome. Please submit issues and pull requests through the project's repository.
