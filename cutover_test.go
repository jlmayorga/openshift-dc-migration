package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestHealthWaitsForRouterConvergence(t *testing.T) {
	for _, recovering := range []bool{true, false} {
		t.Run(fmt.Sprint(recovering), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if requests.Add(1) < 3 || !recovering {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			r := NewRunner(DefaultOptions())
			r.PollInterval = time.Millisecond
			r.Options.ReadinessTimeout = 50 * time.Millisecond
			p := &MigrationPlan{HealthURL: server.URL, Desired: &appsv1.Deployment{}}
			one := int32(1)
			p.Desired.Spec.Replicas = &one
			err := r.checkHealth(context.Background(), p)
			if recovering && err != nil {
				t.Fatal(err)
			}
			if !recovering && (err == nil || !strings.Contains(err.Error(), "HTTP 503")) {
				t.Fatalf("persistent failure lost: %v", err)
			}
			if requests.Load() < 3 {
				t.Fatal("health was not retried")
			}
		})
	}
}

func cutoverTest(t *testing.T, mode string, dependencies bool) (*Runner, *contractCluster, string) {
	t.Helper()
	r, c, path := stageTest(t)
	p, _ := loadPlan(path)
	p.HealthURL = "https://health.example/ready"
	p.CutoverMode = mode
	p.OverlapJustification = "stateless HTTP service permits parallel replicas"
	c.expectOverlap = mode == "overlap"
	if dependencies {
		c.services = []corev1.Service{{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "example", UID: "service-uid"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"deploymentconfig": "web"}}}}
		one := int32(1)
		c.hpas = []autoscalingv2.HorizontalPodAutoscaler{{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "example", UID: "hpa-uid", ResourceVersion: "1"}, Spec: autoscalingv2.HorizontalPodAutoscalerSpec{ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: "apps.openshift.io/v1", Kind: "DeploymentConfig", Name: "web"}, MinReplicas: &one, MaxReplicas: 3}}}
		p.Services = append(p.Services, c.services...)
		p.HPAs = append(p.HPAs, c.hpas...)
	}
	if err := savePlan(path, p); err != nil {
		t.Fatal(err)
	}
	r.HealthCheck = func(context.Context, string) error { return nil }
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.Options.Mode = "cutover"
	r.Options.AutomationQuiesced = true
	r.Options.ReadinessTimeout = 200 * time.Millisecond
	return r, c, path
}
func TestSuccessfulCutoverAndRecovery(t *testing.T) {
	for _, mode := range []string{"exclusive", "overlap"} {
		t.Run(mode, func(t *testing.T) {
			r, c, path := cutoverTest(t, mode, true)
			original := fixture(t)
			if err := r.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			p, _ := loadPlan(path)
			if p.Progress.Phase != "complete" || !r.Report.Results[0].Ready {
				t.Fatal(p.Progress)
			}
			if c.hpas[0].Spec.ScaleTargetRef.Kind != "Deployment" {
				t.Fatal("HPA not retargeted")
			}
			mutations := len(c.mutations)
			if err := r.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(c.mutations) != mutations {
				t.Fatal("completed rerun mutated cluster")
			}
			r.Options.Mode = "rollback"
			if err := r.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(c.source.Object["spec"], original.Object["spec"]) || *c.destination.Spec.Replicas != 0 || !c.destination.Spec.Paused || c.hpas[0].Spec.ScaleTargetRef.Kind != "DeploymentConfig" {
				t.Fatalf("bad recovery: %v", c.mutations)
			}
			if err := r.Run(context.Background()); err != nil {
				t.Fatal("recovery rerun", err)
			}
		})
	}
}
func TestFailedCutoverRecovery(t *testing.T) {
	for _, scenario := range []string{"rollout", "health", "retirement", "cancel", "connectivity", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			r, c, path := cutoverTest(t, "exclusive", false)
			ctx := context.Background()
			cancel := func() {}
			switch scenario {
			case "rollout":
				c.failRollout = true
			case "health":
				r.HealthCheck = func(context.Context, string) error { return errors.New("unhealthy") }
			case "retirement":
				c.blockRetirement = true
			case "cancel":
				ctx, cancel = context.WithCancel(ctx)
				c.cancelOnActivate = cancel
			case "connectivity":
				c.loseConnection = true
			case "timeout":
				c.pendingRollout = true
			}
			defer cancel()
			if err := r.Run(ctx); err == nil {
				t.Fatal("failure swallowed")
			}
			p, _ := loadPlan(path)
			if p.Progress.Phase == "complete" || r.Report.Results[0].Ready {
				t.Fatal("failure reported ready")
			}
			if scenario == "retirement" && *c.destination.Spec.Replicas != 0 {
				t.Fatal("activated before retirement")
			}
			c.lost = false
			c.loseConnection = false
			c.blockRetirement = false
			c.cancelOnActivate = nil
			c.failRollout = false
			c.pendingRollout = false
			r.HealthCheck = func(context.Context, string) error { return nil }
			r.Options.Mode = "rollback"
			if err := r.Run(context.Background()); err != nil {
				t.Fatal("recovery", err)
			}
			replicas, _, _ := unstructured.NestedInt64(c.source.Object, "spec", "replicas")
			if replicas != 1 || *c.destination.Spec.Replicas != 0 {
				t.Fatal("recovery ordering violated")
			}
		})
	}
}
func TestCutoverRequiresAcknowledgments(t *testing.T) {
	r, c, path := cutoverTest(t, "exclusive", false)
	r.Options.AutomationQuiesced = false
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("missing automation acknowledgment accepted")
	}
	if len(c.mutations) != 0 {
		t.Fatal("mutated without acknowledgment")
	}
	p, _ := loadPlan(path)
	p.HealthURL = ""
	_ = savePlan(path, p)
	r.Options.AutomationQuiesced = true
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("missing health check accepted")
	}
}

func TestRecoveryStopsChangedImage(t *testing.T) {
	r, c, _ := cutoverTest(t, "exclusive", false)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.destination.Spec.Template.Spec.Containers[0].Image = "registry.example/new:triggered"
	r.Options.Mode = "rollback"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *c.destination.Spec.Replicas != 0 {
		t.Fatal("image update prevented safe stop")
	}
}
func TestLostRecoveryNeverClaimsSuccess(t *testing.T) {
	r, c, path := cutoverTest(t, "exclusive", false)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.lost = true
	r.Options.Mode = "rollback"
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("lost connectivity marked recovered")
	}
	p, _ := loadPlan(path)
	if p.Progress.Phase == "rolled-back" || r.Report.Results[0].Outcome == "restored" {
		t.Fatal("unverified recovery reported success")
	}
}

func TestOrphanedDestinationBlocksSourceRestore(t *testing.T) {
	r, c, _ := cutoverTest(t, "exclusive", false)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.orphanLabels = c.destination.Spec.Template.Labels
	c.destination = nil
	r.Options.Mode = "rollback"
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("orphan pods ignored during recovery")
	}
	n, _, _ := unstructured.NestedInt64(c.source.Object, "spec", "replicas")
	if n != 0 {
		t.Fatal("source restored while destination pods still exist")
	}
}

func TestTargetRevalidatedBeforeSourceMutation(t *testing.T) {
	for _, scenario := range []string{"changed-spec", "changed-uid", "deleting"} {
		t.Run(scenario, func(t *testing.T) {
			r, c, _ := cutoverTest(t, "exclusive", false)
			switch scenario {
			case "changed-spec":
				c.destination.Spec.Template.Spec.Containers[0].Image = "unrelated:image"
			case "changed-uid":
				c.destination.UID = "replacement"
			case "deleting":
				now := metav1.Now()
				c.destination.DeletionTimestamp = &now
			}
			if err := r.Run(context.Background()); err == nil {
				t.Fatal("changed target accepted")
			}
			if len(c.mutations) != 0 || c.sourceReplicas != 1 {
				t.Fatal("source changed before target revalidation")
			}
		})
	}
}
func TestDeletingHPACannotBeReportedRestored(t *testing.T) {
	r, c, path := cutoverTest(t, "exclusive", true)
	now := metav1.Now()
	c.hpas[0].DeletionTimestamp = &now
	p, _ := loadPlan(path)
	if r.createHPA(context.Background(), p, p.HPAs[0], true) == nil {
		t.Fatal("deleting HPA reported restored")
	}
}
