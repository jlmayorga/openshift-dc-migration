package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestConvertDCtoDeployment(t *testing.T) {
	// Create a sample DeploymentConfig
	dc := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps.openshift.io/v1",
			"kind":       "DeploymentConfig",
			"metadata": map[string]interface{}{
				"name":      "test-dc",
				"namespace": "test-namespace",
			},
			"spec": map[string]interface{}{
				"replicas": int64(3),
				"selector": map[string]interface{}{
					"app": "test-app",
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": "test-app",
						},
					},
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "test-container",
								"image": "test-image:latest",
							},
						},
					},
				},
			},
		},
	}

	// Convert DC to Deployment
	deployment, err := convertDCtoDeployment(dc)

	// Assert no error occurred
	assert.NoError(t, err)

	// Assert the converted Deployment has the correct structure
	assert.Equal(t, "apps/v1", deployment.GetAPIVersion())
	assert.Equal(t, "Deployment", deployment.GetKind())
	assert.Equal(t, "test-dc", deployment.GetName())
	assert.Equal(t, "test-namespace", deployment.GetNamespace())

	// Check replicas
	replicas, found, err := unstructured.NestedInt64(deployment.Object, "spec", "replicas")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, int64(3), replicas)

	// Check selector
	selector, found, err := unstructured.NestedMap(deployment.Object, "spec", "selector", "matchLabels")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, map[string]interface{}{"app": "test-app"}, selector)

	// Check template
	template, found, err := unstructured.NestedMap(deployment.Object, "spec", "template")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.NotNil(t, template)
}

func TestCopyMetadata(t *testing.T) {
	// Create sample DC and Deployment
	dc := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "test-dc",
				"namespace": "test-namespace",
				"labels": map[string]interface{}{
					"app": "test-app",
				},
				"annotations": map[string]interface{}{
					"openshift.io/generated-by": "OpenShiftWebConsole",
				},
			},
		},
	}

	deployment := &unstructured.Unstructured{
		Object: map[string]interface{}{},
	}

	// Set global variables for this test
	preserveLabels = true
	preserveAnnotations = true

	// Copy metadata
	err := copyMetadata(dc, deployment)

	// Assert no error occurred
	assert.NoError(t, err)

	// Check copied metadata
	metadata, found, err := unstructured.NestedMap(deployment.Object, "metadata")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "test-dc", metadata["name"])
	assert.Equal(t, "test-namespace", metadata["namespace"])

	// Check labels
	labels, found, err := unstructured.NestedMap(deployment.Object, "metadata", "labels")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, map[string]interface{}{"app": "test-app"}, labels)

	// Check annotations
	annotations, found, err := unstructured.NestedMap(deployment.Object, "metadata", "annotations")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Contains(t, annotations, "openshift.io/generated-by")
	assert.Contains(t, annotations, "openshift.io/migration-timestamp")
}

// Add more tests for other functions in converter.go
