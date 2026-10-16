package main

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func adminPolicyFixture(resource, position, label string) unstructured.Unstructured {
	kind := "AdminNetworkPolicy"
	if resource == "baselineadminnetworkpolicies" {
		kind = "BaselineAdminNetworkPolicy"
	}
	selection := map[string]interface{}{"namespaceSelector": map[string]interface{}{}, "podSelector": map[string]interface{}{"matchExpressions": []interface{}{map[string]interface{}{"key": label, "operator": "Exists"}}}}
	spec := map[string]interface{}{"subject": map[string]interface{}{"namespaces": map[string]interface{}{}}, "ingress": []interface{}{map[string]interface{}{"action": "Deny", "from": []interface{}{map[string]interface{}{"namespaces": map[string]interface{}{}}}}}}
	if position == "subject" {
		spec["subject"] = map[string]interface{}{"pods": selection}
	} else {
		direction, peer := "ingress", "from"
		if position == "egress-peer" {
			direction, peer = "egress", "to"
		}
		spec[direction] = []interface{}{map[string]interface{}{"action": "Deny", peer: []interface{}{map[string]interface{}{"pods": selection}}}}
	}
	return unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "policy.networking.k8s.io/v1alpha1", "kind": kind, "metadata": map[string]interface{}{"name": "guard"}, "spec": spec}}
}

func TestAdminPolicyChangeBlocksBeforeSourceRetirement(t *testing.T) {
	for _, resource := range []string{"adminnetworkpolicies", "baselineadminnetworkpolicies"} {
		for _, position := range []string{"subject", "ingress-peer", "egress-peer"} {
			for _, label := range []string{migrationLabel, "deployment", "pod-template-hash"} {
				for _, mode := range []string{"preflight", "cutover"} {
					t.Run(resource+"/"+position+"/"+label+"/"+mode, func(t *testing.T) {
						r, c, path := cutoverTest(t, "exclusive", true)
						c.adminPolicyVersion = "v1alpha1"
						c.adminPolicies = map[string][]unstructured.Unstructured{resource: {adminPolicyFixture(resource, position, label)}}
						source, destination, hpa := c.source.DeepCopy(), c.destination.DeepCopy(), c.hpas[0].DeepCopy()
						r.Options.Mode = mode
						err := r.Run(context.Background())
						if err == nil || !strings.Contains(err.Error(), "guard") {
							t.Fatal("admin policy label change was not diagnosed", err)
						}
						p, _ := loadPlan(path)
						if len(c.mutations) != 0 || !reflect.DeepEqual(c.source, source) || !reflect.DeepEqual(c.destination, destination) || !reflect.DeepEqual(c.hpas[0], *hpa) || p.Progress.Phase != "staged" {
							t.Fatal("source or autoscaler changed before admin policy assessment", c.mutations)
						}
					})
				}
			}
		}
	}
}

func TestAdminPolicyAssessmentFailureIsNotAbsence(t *testing.T) {
	for _, scenario := range []string{"discovery-denied", "anp-denied", "banp-denied", "namespace-denied", "unknown-version", "unknown-field", "malformed-selector", "missing-spec", "later-page", "repeated-page"} {
		t.Run(scenario, func(t *testing.T) {
			r, c, path := cutoverTest(t, "exclusive", true)
			c.adminPolicyVersion = "v1alpha1"
			policy := adminPolicyFixture("adminnetworkpolicies", "subject", migrationLabel)
			c.adminPolicies = map[string][]unstructured.Unstructured{"adminnetworkpolicies": {policy}}
			switch scenario {
			case "discovery-denied":
				c.denyConstraintRead = "policy.networking.k8s.io"
			case "anp-denied":
				c.denyConstraintRead = "adminnetworkpolicies"
			case "banp-denied":
				c.adminPolicies = nil
				c.denyConstraintRead = "baselineadminnetworkpolicies"
			case "namespace-denied":
				c.denyConstraintRead = "namespace"
			case "unknown-version":
				c.adminPolicyVersion = "v1alpha2"
			case "unknown-field":
				_ = unstructured.SetNestedField(policy.Object, true, "spec", "futureSelection")
			case "malformed-selector":
				_ = unstructured.SetNestedField(policy.Object, true, "spec", "subject", "pods", "podSelector", "futureExpression")
			case "missing-spec":
				delete(policy.Object, "spec")
			case "later-page", "repeated-page":
				c.mutateResponse = func(req *http.Request, object map[string]interface{}) {
					if strings.HasSuffix(req.URL.Path, "/adminnetworkpolicies") {
						if req.URL.Query().Get("continue") == "" || scenario == "repeated-page" {
							object["items"] = []interface{}{}
							object["metadata"] = map[string]interface{}{"continue": "second-page"}
						}
					}
				}
			}
			source, destination, hpa := c.source.DeepCopy(), c.destination.DeepCopy(), c.hpas[0].DeepCopy()
			r.Options.Mode = "preflight"
			err := r.Run(context.Background())
			if err == nil {
				t.Fatal("could not verify cluster policies but allowed migration")
			}
			p, _ := loadPlan(path)
			if len(c.mutations) != 0 || !reflect.DeepEqual(c.source, source) || !reflect.DeepEqual(c.destination, destination) || !reflect.DeepEqual(c.hpas[0], *hpa) || p.Progress.Phase != "staged" {
				t.Fatal("failed assessment changed workload or autoscaler")
			}
			last := r.Report.Results[0].Constraints.Checks
			if last[len(last)-1].Name != "admin-network-policy-selection" {
				t.Fatal("test failed before reaching intended assessment", err)
			}
		})
	}
}

func TestEquivalentAdminPolicySelectionAllowsMigration(t *testing.T) {
	for _, scenario := range []string{"absent", "empty", "same-subject", "same-peer", "other-namespace", "namespace-only"} {
		t.Run(scenario, func(t *testing.T) {
			r, c, _ := cutoverTest(t, "exclusive", true)
			c.adminPolicyVersion = "v1alpha1"
			policy := adminPolicyFixture("adminnetworkpolicies", "subject", "deploymentconfig")
			switch scenario {
			case "absent":
				c.adminPolicyVersion = ""
			case "same-peer":
				policy = adminPolicyFixture("adminnetworkpolicies", "ingress-peer", "deploymentconfig")
			case "other-namespace":
				policy = adminPolicyFixture("adminnetworkpolicies", "subject", migrationLabel)
				_ = unstructured.SetNestedMap(policy.Object, map[string]interface{}{"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": "unrelated"}}, "spec", "subject", "pods", "namespaceSelector")
			case "namespace-only":
				_ = unstructured.SetNestedMap(policy.Object, map[string]interface{}{"namespaces": map[string]interface{}{}}, "spec", "subject")
			}
			if scenario != "empty" && scenario != "absent" {
				c.adminPolicies = map[string][]unstructured.Unstructured{"adminnetworkpolicies": {policy}}
			}
			if err := r.Run(context.Background()); err != nil {
				t.Fatal("equivalent selection prevented migration", err)
			}
			r.Options.Mode = "rollback"
			c.denyConstraintRead = "policy.networking.k8s.io"
			if err := r.Run(context.Background()); err != nil {
				t.Fatal("new constraint prevented explicit recovery", err)
			}
		})
	}
}
