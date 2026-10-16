package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	o := DefaultOptions()
	o.OutputDir = t.TempDir()
	o.ReportPath = filepath.Join(o.OutputDir, "report.json")
	o.PDFPath = ""
	return o
}
func liveTestRunner(t *testing.T, handler http.HandlerFunc) *Runner {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	o := testOptions(t)
	o.Projects = []string{"example"}
	o.Kubeconfig = filepath.Join(t.TempDir(), "config")
	data := fmt.Sprintf("apiVersion: v1\nkind: Config\ncurrent-context: test\ncontexts:\n- name: test\n  context:\n    cluster: local\n    user: test\nclusters:\n- name: local\n  cluster:\n    server: %s\nusers:\n- name: test\n  user: {}\n", server.URL)
	if err := os.WriteFile(o.Kubeconfig, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return NewRunner(o)
}
func jsonResponse(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func discoveryResponse(w http.ResponseWriter, req *http.Request) bool {
	resources := map[string]string{"/apis/apps.openshift.io/v1": "deploymentconfigs", "/apis/apps/v1": "deployments", "/api/v1": "pods", "/apis/authorization.k8s.io/v1": "selfsubjectaccessreviews"}
	if resource, ok := resources[req.URL.Path]; ok {
		jsonResponse(w, map[string]interface{}{"kind": "APIResourceList", "apiVersion": "v1", "groupVersion": strings.TrimPrefix(req.URL.Path, "/apis/"), "resources": []interface{}{map[string]interface{}{"name": resource, "namespaced": true, "kind": "Object", "verbs": []string{"get", "list", "create"}}}})
		return true
	}
	return false
}
func TestHTTPReadOnlyContracts(t *testing.T) {
	dc := fixture(t)
	var lists, reviews atomic.Int32
	r := liveTestRunner(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Accept") != "application/json" {
			t.Errorf("non JSON accept %q", req.Header.Get("Accept"))
		}
		if discoveryResponse(w, req) {
			return
		}
		switch req.URL.Path {
		case "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews":
			reviews.Add(1)
			if req.Header.Get("Content-Type") != "application/json" {
				t.Errorf("non JSON request")
			}
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			attr := body["spec"].(map[string]interface{})["resourceAttributes"].(map[string]interface{})
			if attr["namespace"] != "example" || attr["verb"] != "list" || attr["resource"] != "deploymentconfigs" {
				t.Errorf("unnecessary permission: %v", attr)
			}
			jsonResponse(w, map[string]interface{}{"kind": "SelfSubjectAccessReview", "apiVersion": "authorization.k8s.io/v1", "status": map[string]interface{}{"allowed": true}})
		case "/apis/apps.openshift.io/v1/namespaces/example/deploymentconfigs":
			lists.Add(1)
			w.Header().Set("Warning", `299 openshift "DeploymentConfig is deprecated"`)
			jsonResponse(w, map[string]interface{}{"kind": "DeploymentConfigList", "apiVersion": "apps.openshift.io/v1", "items": []interface{}{dc.Object}})
		default:
			t.Errorf("unexpected write or namespace-object access: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	})
	r.Options.Projects = []string{"openshift-monitoring", "example", "example"}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lists.Load() != 1 || reviews.Load() != 1 || r.Report.Totals.Generated != 1 || r.Report.Totals.Skipped != 1 || r.Report.Context != "test" {
		t.Fatalf("%+v lists=%d reviews=%d", r.Report, lists.Load(), reviews.Load())
	}
}
func TestHTTPFailuresAndCancellation(t *testing.T) {
	for _, scenario := range []string{"missing-api", "discovery-denied", "access-denied", "expired", "list-denied", "empty", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			r := liveTestRunner(t, func(w http.ResponseWriter, req *http.Request) {
				if strings.HasPrefix(req.URL.Path, "/apis/apps.openshift.io/v1") && !strings.Contains(req.URL.Path, "/namespaces/") {
					if scenario == "missing-api" {
						http.Error(w, "missing", 404)
						return
					}
					if scenario == "discovery-denied" {
						http.Error(w, "forbidden", 403)
						return
					}
					if scenario == "expired" {
						http.Error(w, "expired", 401)
						return
					}
					if scenario == "cancel" {
						<-req.Context().Done()
						return
					}
				}
				if discoveryResponse(w, req) {
					return
				}
				if strings.HasSuffix(req.URL.Path, "selfsubjectaccessreviews") {
					jsonResponse(w, map[string]interface{}{"kind": "SelfSubjectAccessReview", "apiVersion": "authorization.k8s.io/v1", "status": map[string]interface{}{"allowed": scenario != "access-denied", "reason": "test denial"}})
					return
				}
				if scenario == "list-denied" {
					http.Error(w, "forbidden", 403)
					return
				}
				jsonResponse(w, map[string]interface{}{"kind": "DeploymentConfigList", "apiVersion": "apps.openshift.io/v1", "items": []interface{}{}})
			})
			r.Options.RequestTimeout = 100 * time.Millisecond
			err := r.Run(context.Background())
			if scenario == "empty" {
				if err != nil || r.Report.Totals.Empty != 1 {
					t.Fatalf("%v %+v", err, r.Report)
				}
			} else if err == nil || r.Report.Totals.Failed != 1 {
				t.Fatalf("failure swallowed: %v %+v", err, r.Report)
			}
			var saved Report
			b, e := os.ReadFile(r.Options.ReportPath)
			if e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(b, &saved); e != nil || len(saved.Results) != 1 {
				t.Fatalf("missing partial report: %v", e)
			}
		})
	}
}

func TestAccessReviewUsesActualResourceVersionAcrossServerDefaults(t *testing.T) {
	for _, serverDefault := range []string{"", "*"} {
		t.Run("default="+serverDefault, func(t *testing.T) {
			dc := fixture(t)
			var lists atomic.Int32
			r := liveTestRunner(t, func(w http.ResponseWriter, req *http.Request) {
				if discoveryResponse(w, req) {
					return
				}
				if strings.HasSuffix(req.URL.Path, "/selfsubjectaccessreviews") {
					var body map[string]interface{}
					_ = json.NewDecoder(req.Body).Decode(&body)
					attrs := body["spec"].(map[string]interface{})["resourceAttributes"].(map[string]interface{})
					version, _ := attrs["version"].(string)
					if version == "" {
						version = serverDefault
					}
					// This authorizer allows the actual DC endpoint, but not a
					// review for an omitted or wildcard API version.
					jsonResponse(w, map[string]interface{}{"kind": "SelfSubjectAccessReview", "apiVersion": "authorization.k8s.io/v1", "status": map[string]interface{}{"allowed": version == "v1", "reason": "only apps.openshift.io/v1 DC list is authorized"}})
					return
				}
				if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/deploymentconfigs") {
					lists.Add(1)
					jsonResponse(w, map[string]interface{}{"kind": "DeploymentConfigList", "apiVersion": "apps.openshift.io/v1", "items": []interface{}{dc.Object}})
					return
				}
				t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
				http.NotFound(w, req)
			})
			if err := r.Run(context.Background()); err != nil || lists.Load() != 1 || r.Report.Totals.Generated != 1 {
				t.Fatal("review disagreed with authorized endpoint", err, r.Report)
			}
		})
	}
}

func TestPermissionReviewMatchesEndpointGroupVersion(t *testing.T) {
	for _, endpoint := range []schema.GroupVersionResource{{Version: "v1", Resource: "pods"}, hpaResource, {Group: adminPolicyGroup, Version: adminPolicyVersion, Resource: "adminnetworkpolicies"}} {
		t.Run(endpoint.Group+"/"+endpoint.Version, func(t *testing.T) {
			r := liveTestRunner(t, func(w http.ResponseWriter, req *http.Request) {
				if !strings.HasSuffix(req.URL.Path, "/selfsubjectaccessreviews") {
					t.Errorf("unexpected request %s", req.URL.Path)
					http.NotFound(w, req)
					return
				}
				var body map[string]interface{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				attrs := body["spec"].(map[string]interface{})["resourceAttributes"].(map[string]interface{})
				allowed := attrs["group"] == endpoint.Group || endpoint.Group == "" && attrs["group"] == nil
				allowed = allowed && attrs["version"] == endpoint.Version && attrs["resource"] == endpoint.Resource
				jsonResponse(w, map[string]interface{}{"kind": "SelfSubjectAccessReview", "apiVersion": "authorization.k8s.io/v1", "status": map[string]interface{}{"allowed": allowed}})
			})
			if err := r.connect(); err != nil {
				t.Fatal(err)
			}
			if err := r.access(context.Background(), "example", endpoint.Group, endpoint.Resource, "get"); err != nil {
				t.Fatal("review version differs from requested endpoint", err)
			}
		})
	}
}
func TestArtifactFailuresAndTotals(t *testing.T) {
	o := testOptions(t)
	o.Inputs = []string{"testdata/ocp-4.14-dc.yaml", "missing-file"}
	r := NewRunner(o)
	if r.Run(context.Background()) == nil || r.Report.Totals.Generated != 1 || r.Report.Totals.Failed != 1 || r.Report.Totals.Applied != 0 {
		t.Fatal(r.Report)
	}
	o.OutputDir = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(o.OutputDir, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	r = NewRunner(o)
	if r.Run(context.Background()) == nil || r.Report.Totals.Generated != 0 {
		t.Fatal("save failure swallowed")
	}
	o = testOptions(t)
	o.Inputs = []string{"testdata/ocp-4.14-dc.yaml"}
	o.PDFPath = o.OutputDir
	r = NewRunner(o)
	if r.Run(context.Background()) == nil || r.Report.Totals.Failed != 1 {
		t.Fatal("PDF error swallowed")
	}
	o = testOptions(t)
	o.Inputs = []string{"testdata/ocp-4.14-dc.yaml"}
	o.ReportPath = o.OutputDir
	r = NewRunner(o)
	if r.Run(context.Background()) == nil {
		t.Fatal("JSON write error swallowed")
	}
}

func TestLogFailureAppearsInCanonicalReport(t *testing.T) {
	o := testOptions(t)
	o.Inputs = []string{"testdata/ocp-4.14-dc.yaml"}
	o.LogPath = o.OutputDir
	r := NewRunner(o)
	if r.Run(context.Background()) == nil {
		t.Fatal("log error swallowed")
	}
	var report Report
	b, err := os.ReadFile(o.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &report); err != nil || report.Totals.Failed != 1 || report.Totals.Generated != 1 {
		t.Fatalf("%v %+v", err, report)
	}
}
