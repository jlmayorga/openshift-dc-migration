package migration

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"reflect"
	"testing"
)

func fixture(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	ds, err := readDCFile("testdata/ocp-4.14-dc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return ds[0]
}
func TestRegressionServiceLabelsAndSelector(t *testing.T) {
	dc := fixture(t)
	before := dc.DeepCopy()
	dep, err := NewRunner(DefaultOptions()).convertDCtoDeployment(dc)
	if err != nil {
		t.Fatal(err)
	}
	sel, _, _ := unstructured.NestedStringMap(dep.Object, "spec", "selector", "matchLabels")
	pods, _, _ := unstructured.NestedStringMap(dep.Object, "spec", "template", "metadata", "labels")
	if len(sel) == 0 || pods["deploymentconfig"] != "web" {
		t.Fatalf("selector=%v pod labels=%v", sel, pods)
	}
	if !reflect.DeepEqual(before, dc) {
		t.Fatal("mutated source")
	}
}
func TestRegressionPreservedFields(t *testing.T) {
	dc := fixture(t)
	for _, key := range []string{"revisionHistoryLimit", "replicas"} {
		_ = unstructured.SetNestedField(dc.Object, int64(0), "spec", key)
	}
	_ = unstructured.SetNestedField(dc.Object, int64(7), "spec", "minReadySeconds")
	_ = unstructured.SetNestedField(dc.Object, true, "spec", "paused")
	dep, err := NewRunner(DefaultOptions()).convertDCtoDeployment(dc)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"minReadySeconds", "revisionHistoryLimit", "replicas", "paused"} {
		if _, found, _ := unstructured.NestedFieldNoCopy(dep.Object, "spec", key); !found {
			t.Errorf("dropped %s", key)
		}
	}
}
func TestRegressionRollingHookDetection(t *testing.T) {
	dc := fixture(t)
	_ = unstructured.SetNestedMap(dc.Object, map[string]interface{}{}, "spec", "strategy", "rollingParams", "pre")
	if !hasLifecycleHooks(dc) {
		t.Fatal("rolling pre hook silently lost")
	}
}
func TestRegressionUnknownStrategy(t *testing.T) {
	dc := fixture(t)
	_ = unstructured.SetNestedField(dc.Object, "Custom", "spec", "strategy", "type")
	if _, err := NewRunner(DefaultOptions()).convertDCtoDeployment(dc); err == nil {
		t.Fatal("Custom strategy silently accepted")
	}
}
