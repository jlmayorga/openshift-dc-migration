package migration

import (
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"reflect"
	"testing"
)

func TestSemanticCases(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(*unstructured.Unstructured)
		manual  bool
		invalid bool
	}{
		{"surge-above-100-percent", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, "150%", "spec", "strategy", "rollingParams", "maxSurge")
		}, false, false},
		{"unavailable-above-100-percent", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, "150%", "spec", "strategy", "rollingParams", "maxUnavailable")
		}, false, true},
		{"missing-empty-label", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, "", "spec", "selector", "missing")
		}, false, true},
		{"reserved-label", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, migrationID(d), "spec", "template", "metadata", "labels", migrationLabel)
		}, false, true},
		{"long-min-ready", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, int64(900), "spec", "minReadySeconds")
		}, false, false},
		{"reserved-controller-hash", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, "old-hash", "spec", "template", "metadata", "labels", "pod-template-hash")
		}, true, false},
		{"pod-finalizer", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(d.Object, []interface{}{"test.example/cleanup"}, "spec", "template", "metadata", "finalizers")
		}, true, false},
		{"normal", func(d *unstructured.Unstructured) {}, false, false},
		{"recreate", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, "Recreate", "spec", "strategy", "type")
		}, false, false},
		{"zero", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, int64(0), "spec", "replicas")
		}, false, false},
		{"paused", func(d *unstructured.Unstructured) { _ = unstructured.SetNestedField(d.Object, true, "spec", "paused") }, false, false},
		{"test", func(d *unstructured.Unstructured) { _ = unstructured.SetNestedField(d.Object, true, "spec", "test") }, true, false},
		{"custom", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, "Custom", "spec", "strategy", "type")
		}, true, false},
		{"unknown", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, "Future", "spec", "strategy", "type")
		}, true, false},
		{"manual", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(d.Object, []interface{}{}, "spec", "triggers")
		}, true, false},
		{"null-default-triggers", func(d *unstructured.Unstructured) { _ = unstructured.SetNestedField(d.Object, nil, "spec", "triggers") }, false, false},
		{"selector-mismatch", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, "wrong", "spec", "selector", "app")
		}, false, true},
		{"bad-replicas", func(d *unstructured.Unstructured) { _ = unstructured.SetNestedField(d.Object, "0", "spec", "replicas") }, false, true},
		{"negative", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, int64(-1), "spec", "replicas")
		}, false, true},
		{"new-pod-field", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, []interface{}{}, "spec", "template", "spec", "schedulingGates")
		}, false, false},
		{"future-pod-field", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, true, "spec", "template", "spec", "futureFeature")
		}, false, true},
		{"bad-surge", func(d *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(d.Object, "lots", "spec", "strategy", "rollingParams", "maxSurge")
		}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := fixture(t)
			tc.edit(d)
			before := d.DeepCopy()
			c, err := NewRunner(DefaultOptions()).Convert(d)
			if (err != nil) != tc.invalid {
				t.Fatalf("error=%v", err)
			}
			if err == nil && (len(c.Unsupported) > 0) != tc.manual {
				t.Fatalf("unsupported=%v", c.Unsupported)
			}
			if tc.manual && !c.Deployment.Spec.Paused {
				t.Fatal("manual output must be paused")
			}
			if !reflect.DeepEqual(before, d) {
				t.Fatal("mutated source")
			}
		})
	}
	for _, params := range []string{"rollingParams", "recreateParams"} {
		for _, hook := range []string{"pre", "mid", "post"} {
			t.Run(params+hook, func(t *testing.T) {
				d := fixture(t)
				_ = unstructured.SetNestedMap(d.Object, map[string]interface{}{}, "spec", "strategy", params, hook)
				c, err := NewRunner(DefaultOptions()).Convert(d)
				if err != nil || len(c.Unsupported) == 0 {
					t.Fatalf("hook not blocked: %v %+v", err, c)
				}
			})
		}
	}
}
func TestImageTriggerTranslation(t *testing.T) {
	d := fixture(t)
	triggers, _, _ := unstructured.NestedSlice(d.Object, "spec", "triggers")
	triggers = append(triggers, map[string]interface{}{"type": "ImageChange", "imageChangeParams": map[string]interface{}{"automatic": true, "containerNames": []interface{}{"web"}, "from": map[string]interface{}{"kind": "ImageStreamTag", "name": "web:latest", "namespace": "images"}}})
	_ = unstructured.SetNestedSlice(d.Object, triggers, "spec", "triggers")
	r := NewRunner(DefaultOptions())
	c, err := r.Convert(d)
	if err != nil || len(c.Unsupported) > 0 {
		t.Fatalf("%v %+v", err, c)
	}
	var translated []imageTrigger
	if err = json.Unmarshal([]byte(c.Deployment.Annotations[imageTriggerAnnotation]), &translated); err != nil {
		t.Fatal(err)
	}
	if len(translated) != 1 || translated[0].Paused || translated[0].From.Namespace != "images" || translated[0].FieldPath != `spec.template.spec.containers[?(@.name=="web")].image` {
		t.Fatal(translated)
	}
	_ = unstructured.SetNestedField(d.Object, true, "spec", "paused")
	c, err = r.Convert(d)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(c.Deployment.Annotations[imageTriggerAnnotation]), &translated)
	if !translated[0].Paused {
		t.Fatal("paused source must pause image controller")
	}
}
func TestMetadataAndDefaults(t *testing.T) {
	d := fixture(t)
	unstructured.RemoveNestedField(d.Object, "spec", "replicas")
	d.SetLabels(map[string]string{"user": "keep"})
	_ = unstructured.SetNestedField(d.Object, "keep", "spec", "template", "metadata", "labels", "openshift.io/deployment-config.name")
	o := DefaultOptions()
	o.PreserveLabels = false
	c, err := NewRunner(o).Convert(d)
	if err != nil {
		t.Fatal(err)
	}
	if *c.Deployment.Spec.Replicas != 0 || c.Deployment.Labels["user"] != "" || c.Deployment.Spec.Template.Labels["openshift.io/deployment-config.name"] != "keep" {
		t.Fatalf("lost pod labels or default: %+v", c.Deployment)
	}
}

func TestLongMinReadyHasValidDeadline(t *testing.T) {
	d := fixture(t)
	_ = unstructured.SetNestedField(d.Object, int64(900), "spec", "minReadySeconds")
	c, err := NewRunner(DefaultOptions()).Convert(d)
	if err != nil {
		t.Fatal(err)
	}
	if c.Deployment.Spec.ProgressDeadlineSeconds == nil || *c.Deployment.Spec.ProgressDeadlineSeconds <= c.Deployment.Spec.MinReadySeconds {
		t.Fatal("default progress deadline invalidates preserved readiness")
	}
}
