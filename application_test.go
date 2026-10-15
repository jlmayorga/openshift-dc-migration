package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type contractCluster struct {
	mu                                    sync.Mutex
	source                                *unstructured.Unstructured
	destination                           *appsv1.Deployment
	creates, dryRuns                      int
	denyCreate, mutateAdmission, conflict bool
}

func (c *contractCluster) serve(t *testing.T, w http.ResponseWriter, req *http.Request) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if req.Header.Get("Accept") != "application/json" {
		t.Errorf("non JSON negotiation: %s", req.Header.Get("Accept"))
	}
	if strings.HasSuffix(req.URL.Path, "selfsubjectaccessreviews") {
		jsonResponse(w, map[string]interface{}{"kind": "SelfSubjectAccessReview", "apiVersion": "authorization.k8s.io/v1", "status": map[string]interface{}{"allowed": true}})
		return
	}
	resources := map[string][]string{"/apis/apps.openshift.io/v1": {"deploymentconfigs"}, "/apis/apps/v1": {"deployments", "replicasets", "statefulsets", "daemonsets"}, "/api/v1": {"pods", "services", "replicationcontrollers", "endpoints"}, "/apis/authorization.k8s.io/v1": {"selfsubjectaccessreviews"}, "/apis/autoscaling/v2": {"horizontalpodautoscalers"}, "/apis/image.openshift.io/v1": {"imagestreamtags"}}
	if names, ok := resources[req.URL.Path]; ok {
		items := []interface{}{}
		for _, name := range names {
			items = append(items, map[string]interface{}{"name": name, "kind": "Object", "verbs": []string{"get", "list", "create", "update"}})
		}
		jsonResponse(w, map[string]interface{}{"kind": "APIResourceList", "apiVersion": "v1", "resources": items})
		return
	}
	if strings.HasSuffix(req.URL.Path, "/deploymentconfigs/web") {
		jsonResponse(w, c.source.Object)
		return
	}
	if strings.HasSuffix(req.URL.Path, "/deployments/web") {
		if c.destination == nil {
			http.Error(w, "not found", 404)
		} else {
			jsonResponse(w, c.destination)
		}
		return
	}
	if strings.HasSuffix(req.URL.Path, "/deployments") && req.Method == http.MethodPost {
		var d appsv1.Deployment
		if err := json.NewDecoder(req.Body).Decode(&d); err != nil {
			t.Error(err)
		}
		if d.Spec.Replicas == nil || *d.Spec.Replicas != 0 || !d.Spec.Paused || d.Annotations[imageTriggerAnnotation] != "" {
			t.Error("unsafe staging request")
		}
		if req.URL.Query().Get("dryRun") == "All" {
			c.dryRuns++
			if c.mutateAdmission {
				v := int32(1)
				d.Spec.Replicas = &v
			}
			jsonResponse(w, &d)
			return
		}
		if c.denyCreate {
			http.Error(w, "quota rejected", 403)
			return
		}
		if c.conflict {
			http.Error(w, "already exists", 409)
			return
		}
		d.UID = "staged-uid"
		d.ResourceVersion = "1"
		c.destination = &d
		c.creates++
		jsonResponse(w, &d)
		return
	}
	if req.Method == http.MethodGet {
		kind := "List"
		version := "v1"
		items := []interface{}{}
		if strings.HasSuffix(req.URL.Path, "/deploymentconfigs") {
			kind = "DeploymentConfigList"
			version = "apps.openshift.io/v1"
			items = append(items, c.source.Object)
		}
		if strings.HasSuffix(req.URL.Path, "/deployments") && c.destination != nil {
			items = append(items, c.destination)
		}
		jsonResponse(w, map[string]interface{}{"kind": kind, "apiVersion": version, "items": items})
		return
	}
	t.Errorf("unexpected mutation %s %s", req.Method, req.URL.Path)
	http.Error(w, "unexpected", 500)
}
func stageTest(t *testing.T) (*Runner, *contractCluster, string) {
	t.Helper()
	cluster := &contractCluster{source: fixture(t)}
	r := liveTestRunner(t, func(w http.ResponseWriter, req *http.Request) { cluster.serve(t, w, req) })
	if err := r.connect(); err != nil {
		t.Fatal(err)
	}
	c, err := r.Convert(cluster.source)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.buildPlan(context.Background(), cluster.source, c, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.Options.OutputDir, "web.plan.json")
	if err = savePlan(path, p); err != nil {
		t.Fatal(err)
	}
	r.Options.Projects = nil
	r.Options.PlanFile = path
	r.Options.Mode = "stage"
	return r, cluster, path
}
func TestStageDryRunAndRerun(t *testing.T) {
	r, c, path := stageTest(t)
	r.Options.Mode = "dry-run"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.creates != 0 || c.dryRuns != 1 {
		t.Fatal("dry run persisted a workload")
	}
	r.Options.Mode = "stage"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.creates != 1 || r.Report.Totals.Applied != 1 {
		t.Fatalf("%+v", r.Report)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.creates != 1 {
		t.Fatal("rerun duplicated create")
	}
	p, err := loadPlan(path)
	if err != nil || p.Progress.Phase != "staged" || p.Progress.DestinationUID != "staged-uid" {
		t.Fatalf("%v %+v", err, p)
	}
}
func TestStageFailures(t *testing.T) {
	for _, scenario := range []string{"stale", "foreign", "denied", "conflict", "admission", "tampered", "wrong-context", "unsupported", "active"} {
		t.Run(scenario, func(t *testing.T) {
			r, c, path := stageTest(t)
			p, _ := loadPlan(path)
			switch scenario {
			case "stale":
				c.source.SetResourceVersion("43")
			case "foreign":
				c.destination = &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "example"}}
			case "denied":
				c.denyCreate = true
			case "conflict":
				c.conflict = true
			case "admission":
				c.mutateAdmission = true
			case "tampered":
				p.Desired.Name = "other"
				_ = savePlan(path, p)
			case "wrong-context":
				p.Context = "production"
				_ = savePlan(path, p)
			case "unsupported":
				_ = unstructured.SetNestedField(p.Source.Object, true, "spec", "test")
				_ = savePlan(path, p)
			case "active":
				if err := r.Run(context.Background()); err != nil {
					t.Fatal(err)
				}
				one := int32(1)
				c.destination.Spec.Replicas = &one
			}
			if err := r.Run(context.Background()); err == nil || r.Report.Totals.Failed != 1 {
				t.Fatalf("%s failure swallowed: %v %+v", scenario, err, r.Report)
			}
			if scenario != "active" && c.creates != 0 {
				t.Fatal("failed validation persisted workload")
			}
		})
	}
}
func TestPlanReadOnlyAndOfflineNotExecutable(t *testing.T) {
	r, c, path := stageTest(t)
	p, _ := loadPlan(path)
	if !p.InventoryComplete || c.creates != 0 {
		t.Fatal("planning wrote workload")
	}
	p.InventoryComplete = false
	_ = savePlan(path, p)
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("executed offline plan")
	}
	if c.creates != 0 {
		t.Fatal("unsafe create")
	}
	_ = fmt.Sprint(p.Recovery)
}
