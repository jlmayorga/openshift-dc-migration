package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func hpaBaselineBehavior(t *testing.T, c *contractCluster, path string) {
	t.Helper()
	p, err := loadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	c.hpas[0].Spec.Behavior = &autoscalingv2.HorizontalPodAutoscalerBehavior{ScaleUp: &autoscalingv2.HPAScalingRules{Policies: []autoscalingv2.HPAScalingPolicy{{Type: autoscalingv2.PodsScalingPolicy, Value: 4, PeriodSeconds: 60}}}}
	p.HPAs[0].Spec = *c.hpas[0].Spec.DeepCopy()
	c.destination.Annotations[planAnnotation] = planDigest(p)
	if err = savePlan(path, p); err != nil {
		t.Fatal(err)
	}
}

func TestRawWorkloadAdmissionCannotHideNewFields(t *testing.T) {
	for _, resource := range []string{"deployment-get", "deployment-update", "hpa-get", "hpa-create", "replicaset-create"} {
		for _, mode := range []string{"preflight", "cutover"} {
			t.Run(resource+"/"+mode, func(t *testing.T) {
				r, c, path := cutoverTest(t, "exclusive", true)
				hpaBaselineBehavior(t, c, path)
				r.Options.Mode = mode
				c.mutateResponse = func(req *http.Request, object map[string]interface{}) {
					url := req.URL.Path
					if resource == "deployment-get" && req.Method == http.MethodGet && strings.HasSuffix(url, "/deployments/web") || resource == "deployment-update" && req.Method == http.MethodPut && strings.HasSuffix(url, "/deployments/web") || resource == "replicaset-create" && req.Method == http.MethodPost && strings.HasSuffix(url, "/replicasets") {
						_ = unstructured.SetNestedMap(object, map[string]interface{}{"requests": map[string]interface{}{"cpu": "900"}}, "spec", "template", "spec", "resources")
					}
					if resource == "hpa-get" && req.Method == http.MethodGet && strings.Contains(url, "/horizontalpodautoscalers/") || resource == "hpa-create" && req.Method == http.MethodPost && strings.HasSuffix(url, "/horizontalpodautoscalers") {
						_ = unstructured.SetNestedField(object, 0.05, "spec", "behavior", "scaleUp", "tolerance")
					}
				}
				source, destination, hpa := c.source.DeepCopy(), c.destination.DeepCopy(), c.hpas[0].DeepCopy()
				err := r.Run(context.Background())
				if err == nil || !strings.Contains(err.Error(), "baseline") {
					t.Fatalf("new raw field was hidden from validation: %v", err)
				}
				if len(c.mutations) != 0 || !reflect.DeepEqual(c.source, source) || !reflect.DeepEqual(c.destination, destination) || !reflect.DeepEqual(c.hpas[0], *hpa) {
					t.Fatal("workload/HPA changed before raw response validation", c.mutations)
				}
			})
		}
	}
}

func TestRawDeploymentCreateRejectsNewFieldsBeforePersistence(t *testing.T) {
	r, c, _ := stageTest(t)
	c.mutateResponse = func(req *http.Request, object map[string]interface{}) {
		if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/deployments") {
			_ = unstructured.SetNestedField(object, "Recursive", "spec", "template", "spec", "securityContext", "seLinuxChangePolicy")
		}
	}
	if err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "seLinuxChangePolicy") {
		t.Fatalf("stage silently discarded an unknown field: %v", err)
	}
	if c.creates != 0 || c.destination != nil {
		t.Fatal("persisted before validating raw dry-run response")
	}
}

func TestRecoveryPreservesUnrecognizedOwnedDestinationFields(t *testing.T) {
	r, c, _ := cutoverTest(t, "exclusive", true)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var lastSpec map[string]interface{}
	var requestSpec map[string]interface{}
	c.inspectRequest = func(req *http.Request, body []byte) {
		if req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/deployments/web") {
			var object map[string]interface{}
			if err := json.Unmarshal(body, &object); err != nil {
				t.Error(err)
			}
			requestSpec, _, _ = unstructured.NestedMap(object, "spec")
			if req.URL.Query().Get("dryRun") != "All" {
				lastSpec = requestSpec
			}
		}
	}
	c.mutateResponse = func(req *http.Request, object map[string]interface{}) {
		if strings.HasSuffix(req.URL.Path, "/deployments/web") {
			if req.Method == http.MethodPut {
				object["spec"] = requestSpec
			} else {
				_ = unstructured.SetNestedField(object, "Recursive", "spec", "template", "spec", "securityContext", "seLinuxChangePolicy")
			}
		}
	}
	r.Options.Mode = "rollback"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal("new raw guard blocked explicit recovery", err)
	}
	policy, _, _ := unstructured.NestedString(lastSpec, "template", "spec", "securityContext", "seLinuxChangePolicy")
	if policy != "Recursive" || lastSpec["paused"] != true || lastSpec["replicas"] != float64(0) {
		t.Fatal("recovery lost live fields or failed to stop destination", lastSpec)
	}
}

func TestNewStatusFieldsDoNotPreventMigration(t *testing.T) {
	r, c, _ := cutoverTest(t, "exclusive", true)
	c.mutateResponse = func(req *http.Request, object map[string]interface{}) {
		if strings.HasSuffix(req.URL.Path, "/deployments/web") {
			_ = unstructured.SetNestedField(object, float64(0), "status", "terminatingReplicas")
		}
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal("new status field blocked baseline-compatible migration", err)
	}
	r.Options.Mode = "rollback"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal("status-only extension blocked recovery", err)
	}
}

func TestPersistedStageResponseRetainsIdentityForRawRecovery(t *testing.T) {
	r, c, path := stageTest(t)
	r.Options.ReadinessTimeout = 200 * time.Millisecond
	// This case starts recovery before cutover has changed the healthy source.
	// Represent that existing availability instead of waiting for a fake update.
	_ = unstructured.SetNestedField(c.source.Object, int64(1), "status", "availableReplicas")
	p, err := loadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	p.HealthURL = "https://health.example/ready"
	if err = savePlan(path, p); err != nil {
		t.Fatal(err)
	}
	r.HealthCheck = func(context.Context, string) error { return nil }
	var requestSpec map[string]interface{}
	c.inspectRequest = func(req *http.Request, body []byte) {
		if req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/deployments/web") {
			var object map[string]interface{}
			_ = json.Unmarshal(body, &object)
			requestSpec, _, _ = unstructured.NestedMap(object, "spec")
		}
	}
	c.mutateResponse = func(req *http.Request, object map[string]interface{}) {
		if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/deployments") && req.URL.Query().Get("dryRun") != "All" || req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/deployments/web") {
			_ = unstructured.SetNestedField(object, "Recursive", "spec", "template", "spec", "securityContext", "seLinuxChangePolicy")
		}
		if req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/deployments/web") {
			object["spec"] = requestSpec
		}
	}
	if err = r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "baseline") {
		t.Fatal("persist-only mutation was not rejected", err)
	}
	p, err = loadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.destination == nil || p.Progress.DestinationUID != string(c.destination.UID) || !r.Report.Results[0].Applied || p.Progress.Phase != "stage-intent" {
		t.Fatal("persisted identity or write outcome was lost")
	}
	r.Options.Mode = "rollback"
	if err = r.Run(context.Background()); err != nil {
		t.Fatal("could not recover the identified paused destination", err)
	}
	if *c.destination.Spec.Replicas != 0 || !c.destination.Spec.Paused {
		t.Fatal("destination was not stopped")
	}
}

func TestPersistedHPAFieldsFailAndOwnedHPAStillRecovers(t *testing.T) {
	r, c, path := cutoverTest(t, "exclusive", true)
	hpaBaselineBehavior(t, c, path)
	c.mutateResponse = func(req *http.Request, object map[string]interface{}) {
		if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/horizontalpodautoscalers") && req.URL.Query().Get("dryRun") != "All" || req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/horizontalpodautoscalers/") {
			kind, _, _ := unstructured.NestedString(object, "spec", "scaleTargetRef", "kind")
			if kind == "Deployment" {
				_ = unstructured.SetNestedField(object, 0.05, "spec", "behavior", "scaleUp", "tolerance")
			}
		}
	}
	if err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "tolerance") {
		t.Fatal("persisted HPA mutation was hidden", err)
	}
	r.Options.Mode = "rollback"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal("unknown owned HPA fields prevented safe deletion/recovery", err)
	}
	if *c.destination.Spec.Replicas != 0 || c.hpas[0].Spec.ScaleTargetRef.Kind != "DeploymentConfig" {
		t.Fatal("recovery incomplete")
	}
}

func TestRecoveryDoesNotOverwriteOriginalHPAWithNewFields(t *testing.T) {
	r, c, path := cutoverTest(t, "exclusive", true)
	hpaBaselineBehavior(t, c, path)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, _ := loadPlan(path)
	c.hpas = []autoscalingv2.HorizontalPodAutoscaler{*p.HPAs[0].DeepCopy()}
	c.mutations = nil
	c.mutateResponse = func(req *http.Request, object map[string]interface{}) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/horizontalpodautoscalers/") {
			_ = unstructured.SetNestedField(object, 0.05, "spec", "behavior", "scaleUp", "tolerance")
		}
	}
	r.Options.Mode = "rollback"
	if err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "tolerance") {
		t.Fatal("claimed restoration despite changed original HPA", err)
	}
	for _, mutation := range c.mutations {
		if strings.HasPrefix(mutation, "hpa:") {
			t.Fatal("overwrote the original changed HPA")
		}
	}
}
