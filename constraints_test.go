package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestConstraintFailureLeavesSourceAndAutoscalerUntouched(t *testing.T) {
	for _, scenario := range []string{"pod-admission", "quota-headroom", "quota-read-denied", "cluster-inventory-denied", "no-nodes", "cluster-quota", "network-policy", "node-capacity", "pod-label", "pod-budget", "runtime-policy", "runtime-peer", "pdb"} {
		t.Run(scenario, func(t *testing.T) {
			r, c, path := cutoverTest(t, "exclusive", true)
			original := c.source.DeepCopy()
			hpa := c.hpas[0].DeepCopy()
			switch scenario {
			case "pod-label":
				c.dropPodLabel = true
			case "pod-budget":
				c.injectPodBudget = true
			case "pod-admission":
				c.denyPodDryRun = true
			case "quota-headroom":
				c.quotas = []corev1.ResourceQuota{{ObjectMeta: metav1.ObjectMeta{Name: "tight"}, Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{"pods": resource.MustParse("0")}}, Status: corev1.ResourceQuotaStatus{Hard: corev1.ResourceList{"pods": resource.MustParse("0")}, Used: corev1.ResourceList{"pods": resource.MustParse("0")}}}}
			case "quota-read-denied":
				c.denyConstraintRead = "resourcequotas"
			case "cluster-inventory-denied":
				c.denyConstraintRead = "nodes"
			case "pdb":
				c.disruptionBudgets = []policyv1.PodDisruptionBudget{{ObjectMeta: metav1.ObjectMeta{Name: "keep-available", Generation: 1}, Spec: policyv1.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"deploymentconfig": "web"}}}, Status: policyv1.PodDisruptionBudgetStatus{ObservedGeneration: 1, DisruptionsAllowed: 0}}}
			case "runtime-peer":
				c.networkPolicies = []networkingv1.NetworkPolicy{{ObjectMeta: metav1.ObjectMeta{Name: "legacy-peer"}, Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{}, Ingress: []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "deployment", Operator: metav1.LabelSelectorOpExists}}}}}}}}}}
			case "runtime-policy":
				c.networkPolicies = []networkingv1.NetworkPolicy{{ObjectMeta: metav1.ObjectMeta{Name: "legacy-rc-label"}, Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "deployment", Operator: metav1.LabelSelectorOpExists}}}}}}
			case "network-policy":
				c.networkPolicies = []networkingv1.NetworkPolicy{{ObjectMeta: metav1.ObjectMeta{Name: "target-denied"}, Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: migrationLabel, Operator: metav1.LabelSelectorOpExists}}}}}}
			case "node-capacity":
				c.constraintNodes = []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "full"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}, Allocatable: corev1.ResourceList{"pods": resource.MustParse("0")}}}}
			case "no-nodes":
				c.constraintNodes = []corev1.Node{}
			case "cluster-quota":
				c.clusterQuotas = []unstructured.Unstructured{{Object: map[string]interface{}{"apiVersion": "quota.openshift.io/v1", "kind": "AppliedClusterResourceQuota", "metadata": map[string]interface{}{"name": "shared"}, "spec": map[string]interface{}{"quota": map[string]interface{}{"hard": map[string]interface{}{"pods": "1"}}}, "status": map[string]interface{}{"total": map[string]interface{}{"hard": map[string]interface{}{"pods": "1"}, "used": map[string]interface{}{"pods": "1"}}}}}}
			}
			err := r.Run(context.Background())
			if err == nil {
				t.Fatal("constraint failure accepted")
			}
			p, _ := loadPlan(path)
			if len(c.mutations) != 0 || !reflect.DeepEqual(c.source.Object["spec"], original.Object["spec"]) || len(c.hpas) != 1 || !reflect.DeepEqual(c.hpas[0], *hpa) || *c.destination.Spec.Replicas != 0 || p.Progress.Phase != "staged" {
				t.Fatalf("source/HPA changed before preflight passed: %v", c.mutations)
			}
			if a := r.Report.Results[0].Constraints; a == nil || a.Status != "blocked" || len(a.Blockers) == 0 {
				t.Fatalf("missing assessment: %+v", r.Report)
			}
		})
	}
}
func TestReadOnlyConstraintPreflightIsNeverReusedAtCutover(t *testing.T) {
	r, c, path := cutoverTest(t, "exclusive", true)
	r.Options.Mode = "preflight"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, _ := loadPlan(path)
	if p.Progress.Phase != "staged" || p.Progress.Constraints.Status != "passed" || len(c.mutations) != 0 || r.Report.Results[0].Applied {
		t.Fatal("preflight changed live state")
	}
	c.denyPodDryRun = true
	r.Options.Mode = "cutover"
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("cached preflight used after constraints changed")
	}
	if len(c.mutations) != 0 {
		t.Fatal("constraint failure retired workload")
	}
}
func TestQuotaFullReplicaDemandAndScopes(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{"cpu": resource.MustParse("250m"), "memory": resource.MustParse("32Mi")}}}}}}
	for _, tc := range []struct {
		name, key, hard, used string
		replicas              int32
		scopes                []corev1.ResourceQuotaScope
		blocked               bool
	}{
		{"one-probe-would-fit", "pods", "3", "1", 3, nil, true},
		{"all-replicas-fit", "pods", "4", "1", 3, nil, false},
		{"cpu-summed", "requests.cpu", "1", "500m", 3, nil, true},
		{"memory-summed", "requests.memory", "128Mi", "32Mi", 3, nil, false},
		{"different-scope", "pods", "0", "0", 3, []corev1.ResourceQuotaScope{corev1.ResourceQuotaScopeBestEffort}, false},
		{"matching-scope", "pods", "0", "0", 3, []corev1.ResourceQuotaScope{corev1.ResourceQuotaScopeNotBestEffort}, true},
		{"new-rs-slot", "count/replicasets.apps", "1", "1", 1, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hard := corev1.ResourceList{corev1.ResourceName(tc.key): resource.MustParse(tc.hard)}
			used := corev1.ResourceList{corev1.ResourceName(tc.key): resource.MustParse(tc.used)}
			err := quotaHeadroom("test", corev1.ResourceQuotaSpec{Hard: hard, Scopes: tc.scopes}, corev1.ResourceQuotaStatus{Hard: hard, Used: used}, pod, tc.replicas)
			if (err != nil) != tc.blocked {
				t.Fatalf("wrong capacity result: %v", err)
			}
		})
	}
	spec := corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{"pods": resource.MustParse("4")}}
	if err := quotaHeadroom("new-policy", spec, corev1.ResourceQuotaStatus{}, pod, 1); err == nil || !strings.Contains(err.Error(), "synchronized") {
		t.Fatal("unsynchronized quota accepted", err)
	}
}
func TestPodAccountingIncludesInitSidecarsAndOverhead(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	container := func(name, cpu string) corev1.Container {
		return corev1.Container{Name: name, Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{"cpu": resource.MustParse(cpu)}}}
	}
	sidecar := container("sidecar", "200m")
	sidecar.RestartPolicy = &always
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{container("web", "500m")}, InitContainers: []corev1.Container{sidecar, container("init", "1")}, Overhead: corev1.ResourceList{"cpu": resource.MustParse("100m")}}}
	resources := podResources(pod, false)
	if q := resources["cpu"]; q.Cmp(resource.MustParse("1300m")) != 0 {
		t.Fatal("init + sidecar + overhead understated", q.String())
	}
}
func TestNodeEligibilityRejectsHardPlacementConstraints(t *testing.T) {
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker", Labels: map[string]string{"zone": "a"}}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
	pod := &corev1.Pod{Spec: corev1.PodSpec{NodeSelector: map[string]string{"zone": "a"}}}
	if !nodeEligible(node, pod) {
		t.Fatal("eligible node rejected")
	}
	node.Spec.Taints = []corev1.Taint{{Key: "maintenance", Effect: corev1.TaintEffectNoSchedule}}
	if nodeEligible(node, pod) {
		t.Fatal("untolerated taint accepted")
	}
	pod.Spec.Tolerations = []corev1.Toleration{{Key: "maintenance", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}
	if !nodeEligible(node, pod) {
		t.Fatal("toleration ignored")
	}
	node.Spec.Unschedulable = true
	if nodeEligible(node, pod) {
		t.Fatal("cordoned node accepted")
	}
}

func TestObservedAllocationsIncludeResizeAndNewPodBudgets(t *testing.T) {
	raw := &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "web", "resources": map[string]interface{}{"requests": map[string]interface{}{"cpu": "100m", "memory": "16Mi"}}}}, "resources": map[string]interface{}{"requests": map[string]interface{}{"memory": "64Mi"}}}, "status": map[string]interface{}{"containerStatuses": []interface{}{map[string]interface{}{"name": "web", "allocatedResources": map[string]interface{}{"cpu": "500m"}}}}}}
	resources, err := observedPodRequests(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cpu := resources["cpu"]; cpu.Cmp(resource.MustParse("500m")) != 0 {
		t.Fatal("resize allocation lost", cpu.String())
	}
	if memory := resources["memory"]; memory.Cmp(resource.MustParse("64Mi")) != 0 {
		t.Fatal("Pod budget lost", memory.String())
	}
}

func TestConstraintPreflightToleratesHPAStatusChurn(t *testing.T) {
	r, c, _ := cutoverTest(t, "exclusive", true)
	c.hpaStatusChurn = true
	r.Options.Mode = "preflight"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal("status-only HPA churn blocked read-only preflight", err)
	}
	if len(c.mutations) != 0 || *c.destination.Spec.Replicas != 0 {
		t.Fatal("read-only assessment mutated workload")
	}
}

func TestOverlapCanPreserveDisruptionBudget(t *testing.T) {
	minimum := intstr.FromInt(1)
	r, c, _ := cutoverTest(t, "overlap", true)
	c.disruptionBudgets = []policyv1.PodDisruptionBudget{{ObjectMeta: metav1.ObjectMeta{Name: "keep-available", Generation: 1}, Spec: policyv1.PodDisruptionBudgetSpec{MinAvailable: &minimum, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"deploymentconfig": "web"}}}, Status: policyv1.PodDisruptionBudgetStatus{ObservedGeneration: 1, DisruptionsAllowed: 0}}}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal("verified overlap rejected despite replacement budget membership", err)
	}
}
