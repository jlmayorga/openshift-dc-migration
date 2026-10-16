package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer/cbor/direct"
)

func TestDynamicJSONWriteContract(t *testing.T) {
	r := liveTestRunner(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Accept") != "application/json" || req.Header.Get("Content-Type") != "application/json" {
			t.Errorf("non-JSON dynamic request: Accept=%q Content-Type=%q", req.Header.Get("Accept"), req.Header.Get("Content-Type"))
		}
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("request body was not JSON: %v", err)
			http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
			return
		}
		jsonResponse(w, body)
	})
	if err := r.connect(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Dynamic.Resource(dcResource).Namespace("example").Create(context.Background(), fixture(t), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestJSONContractIgnoresAmbientCBORFeatureFlags(t *testing.T) {
	// client-go reads these flags once per process. A child makes the test
	// independent of other tests without changing shared feature-gate state.
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-test.run=^(TestHTTPReadOnlyContracts|TestDynamicJSONWriteContract|TestDynamicClientRejectsCBORResponse)$")
	command.Env = append(os.Environ(), "KUBE_FEATURE_ClientsAllowCBOR=true", "KUBE_FEATURE_ClientsPreferCBOR=true")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ambient client feature flags changed the wire contract: %v\n%s", err, output)
	}
}

func TestDynamicClientRejectsCBORResponse(t *testing.T) {
	body, err := direct.Marshal(fixture(t).Object)
	if err != nil {
		t.Fatal(err)
	}
	r := liveTestRunner(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Accept") != "application/json" {
			t.Errorf("non-JSON Accept: %q", req.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/cbor")
		_, _ = w.Write(body)
	})
	if err := r.connect(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Dynamic.Resource(dcResource).Namespace("example").Get(context.Background(), "web", metav1.GetOptions{}); err == nil {
		t.Fatal("client accepted a non-JSON response")
	}
}
