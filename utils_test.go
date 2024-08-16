package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIsReservedNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		expected  bool
	}{
		{"Reserved namespace - openshift-infra", "openshift-infra", true},
		{"Reserved namespace - kube-system", "kube-system", true},
		{"OpenShift system namespace", "openshift-monitoring", true},
		{"Kubernetes system namespace", "kube-public", true},
		{"User namespace", "my-project", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isReservedNamespace(tt.namespace)
			assert.Equal(t, tt.expected, result, "Namespace: %s", tt.namespace)
		})
	}
}

func TestHasTriggers(t *testing.T) {
	dcWithTriggers := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"triggers": []interface{}{
					map[string]interface{}{
						"type": "ConfigChange",
					},
				},
			},
		},
	}

	dcWithoutTriggers := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{},
		},
	}

	assert.True(t, hasTriggers(dcWithTriggers))
	assert.False(t, hasTriggers(dcWithoutTriggers))
}

func TestHasLifecycleHooks(t *testing.T) {
	dcWithHooks := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"strategy": map[string]interface{}{
					"recreateParams": map[string]interface{}{
						"pre": map[string]interface{}{},
					},
				},
			},
		},
	}

	dcWithoutHooks := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"strategy": map[string]interface{}{},
			},
		},
	}

	assert.True(t, hasLifecycleHooks(dcWithHooks))
	assert.False(t, hasLifecycleHooks(dcWithoutHooks))
}

func TestHasAutoRollbacks(t *testing.T) {
	dcWithAutoRollbacks := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"strategy": map[string]interface{}{
					"rollingParams": map[string]interface{}{
						"autoRollbackEnabled": true,
					},
				},
			},
		},
	}

	dcWithoutAutoRollbacks := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"strategy": map[string]interface{}{
					"rollingParams": map[string]interface{}{},
				},
			},
		},
	}

	assert.True(t, hasAutoRollbacks(dcWithAutoRollbacks))
	assert.False(t, hasAutoRollbacks(dcWithoutAutoRollbacks))
}

func TestUsesCustomStrategies(t *testing.T) {
	dcWithCustomStrategy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"strategy": map[string]interface{}{
					"type": "Custom",
				},
			},
		},
	}

	dcWithoutCustomStrategy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"strategy": map[string]interface{}{
					"type": "Rolling",
				},
			},
		},
	}

	assert.True(t, usesCustomStrategies(dcWithCustomStrategy))
	assert.False(t, usesCustomStrategies(dcWithoutCustomStrategy))
}

// Add more tests for other functions in utils.go
