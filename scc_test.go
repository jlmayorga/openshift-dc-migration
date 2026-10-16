package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func pinnedSCCTest(t *testing.T) (*Runner, *contractCluster, string) {
	t.Helper()
	r, c, path := cutoverTest(t, "exclusive", true)
	p, err := loadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = unstructured.SetNestedField(p.Source.Object, "pinned", "spec", "template", "metadata", "annotations", "openshift.io/required-scc"); err != nil {
		t.Fatal(err)
	}
	if err = unstructured.SetNestedField(p.Source.Object, "pinned-worker", "spec", "template", "spec", "serviceAccountName"); err != nil {
		t.Fatal(err)
	}
	c.source = p.Source.DeepCopy()
	p.Desired.Spec.Template.Annotations = ensureMap(p.Desired.Spec.Template.Annotations)
	p.Desired.Spec.Template.Annotations["openshift.io/required-scc"] = "pinned"
	p.Desired.Spec.Template.Spec.ServiceAccountName = "pinned-worker"
	c.destination.Spec.Template.Annotations = ensureMap(c.destination.Spec.Template.Annotations)
	c.destination.Spec.Template.Annotations["openshift.io/required-scc"] = "pinned"
	c.destination.Spec.Template.Spec.ServiceAccountName = "pinned-worker"
	c.destination.Annotations[planAnnotation] = planDigest(p)
	p.Progress.AdmittedSpec = c.destination.Spec.DeepCopy()
	if err = savePlan(path, p); err != nil {
		t.Fatal(err)
	}
	return r, c, path
}

func TestRequiredSCCCannotUseCallerPrivilegesAsServiceAccountProof(t *testing.T) {
	for _, failure := range []string{"denied", "uncertain", "contradictory", "permission", "review-error", "missing-api", "wrong-admitted-scc", "missing-admitted-scc", "lost-required-scc"} {
		for _, mode := range []string{"preflight", "cutover"} {
			t.Run(failure+"/"+mode, func(t *testing.T) {
				r, c, path := pinnedSCCTest(t)
				r.Options.Mode = mode
				switch failure {
				case "denied":
					c.localSCCReviewStatus = &authorizationv1.SubjectAccessReviewStatus{Allowed: false}
				case "uncertain":
					c.localSCCReviewStatus = &authorizationv1.SubjectAccessReviewStatus{Allowed: true, EvaluationError: "webhook unavailable"}
				case "contradictory":
					c.localSCCReviewStatus = &authorizationv1.SubjectAccessReviewStatus{Allowed: true, Denied: true}
				case "permission":
					c.deniedLocalSCCReviewAccess = true
				case "review-error":
					c.rejectLocalSCCReviewRequest = true
				case "missing-api":
					c.omitLocalSCCReviewAPI = true
				case "wrong-admitted-scc":
					name := "other"
					c.podSCCOverride = &name
				case "missing-admitted-scc":
					name := ""
					c.podSCCOverride = &name
				case "lost-required-scc":
					c.dropRequiredSCC = true
				}
				source, destination, hpa := c.source.DeepCopy(), c.destination.DeepCopy(), c.hpas[0].DeepCopy()
				if err := r.Run(context.Background()); err == nil {
					t.Fatal("required SCC failure accepted using caller dry-run permission")
				}
				p, err := loadPlan(path)
				if err != nil {
					t.Fatal(err)
				}
				if len(c.mutations) != 0 || !reflect.DeepEqual(c.source, source) || !reflect.DeepEqual(c.destination, destination) || !reflect.DeepEqual(c.hpas[0], *hpa) || p.Progress.Phase != "staged" || p.Progress.Constraints == nil || p.Progress.Constraints.Status != "blocked" {
					t.Fatal("source/HPA/staging state changed before SCC proof", c.mutations)
				}
			})
		}
	}
}

func TestPinnedSCCUsesItsOwnDefaultsAfterServiceAccountAuthorization(t *testing.T) {
	r, c, _ := pinnedSCCTest(t)
	c.mutateSCCSpec = func(spec map[string]interface{}) {
		// These defaults belong to a different SCC. They must not contaminate
		// the dry run of the workload's required SCC, even if they look valid.
		spec["securityContext"] = map[string]interface{}{"runAsUser": int64(42), "seLinuxChangePolicy": "Recursive"}
	}
	c.mutatePodSpec = func(spec map[string]interface{}) {
		if security, ok := spec["securityContext"].(map[string]interface{}); ok {
			if _, found := security["runAsUser"]; found {
				t.Error("unrelated SCC defaults were replayed into the pinned SCC dry run")
			}
		}
		spec["securityContext"] = map[string]interface{}{"runAsUser": int64(1000000)}
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.localSCCReviewCalls != 1 || !r.Report.Results[0].Ready {
		t.Fatal("required-SCC identity was not proved")
	}
	r.Options.Mode = "rollback"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal("SCC proof prevented recovery", err)
	}
}

func TestMatchingOrUnpinnedSCCNeedsNoAdditionalReviewPermission(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(map[bool]string{false: "unpinned", true: "matching-review"}[pinned], func(t *testing.T) {
			var r *Runner
			var c *contractCluster
			if pinned {
				r, c, _ = pinnedSCCTest(t)
				c.reviewedSCC = "pinned"
			} else {
				r, c, _ = cutoverTest(t, "exclusive", true)
			}
			c.deniedLocalSCCReviewAccess = true
			r.Options.Mode = "preflight"
			if err := r.Run(context.Background()); err != nil {
				t.Fatal("unnecessary LocalSubjectAccessReview permission", err)
			}
			if c.localSCCReviewCalls != 0 || len(c.mutations) != 0 || !strings.Contains(r.Report.Results[0].Outcome, "validated") {
				t.Fatal("ordinary SCC assessment changed")
			}
		})
	}
}
