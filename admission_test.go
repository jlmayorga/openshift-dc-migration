package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestAdmissionFieldsCannotBypassBaselineOrPreRetirementChecks(t *testing.T) {
	fields := []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{"image", func(spec map[string]interface{}) {
			spec["volumes"] = []interface{}{map[string]interface{}{"name": "artifact", "image": map[string]interface{}{"reference": "registry.example.test/artifact:v1", "pullPolicy": "IfNotPresent"}}}
		}},
		{"supplementalGroupsPolicy", func(spec map[string]interface{}) {
			spec["securityContext"] = map[string]interface{}{"supplementalGroupsPolicy": "Strict"}
		}},
		{"seLinuxChangePolicy", func(spec map[string]interface{}) {
			spec["securityContext"] = map[string]interface{}{"seLinuxChangePolicy": "Recursive"}
		}},
		// Newer admission translates a legacy AppArmor annotation into this
		// post-floor field. Validate the raw response before typed decoding.
		{"appArmorProfile", func(spec map[string]interface{}) {
			container := spec["containers"].([]interface{})[0].(map[string]interface{})
			container["securityContext"] = map[string]interface{}{"appArmorProfile": map[string]interface{}{"type": "Localhost", "localhostProfile": "migration-fixture"}}
		}},
		{"operator", func(spec map[string]interface{}) {
			spec["tolerations"] = []interface{}{map[string]interface{}{"key": "generation", "operator": "Gt", "value": "2", "effect": "NoSchedule"}}
		}},
	}
	for _, field := range fields {
		for _, scope := range []string{"scc", "pod"} {
			for _, mode := range []string{"preflight", "cutover"} {
				t.Run(field.name+"/"+scope+"/"+mode, func(t *testing.T) {
					r, c, path := cutoverTest(t, "exclusive", true)
					r.Options.Mode = mode
					expectedCheck := "pod-admission-and-limits"
					if scope == "scc" {
						c.mutateSCCSpec = field.mutate
						expectedCheck = "service-account-scc"
					} else {
						c.mutatePodSpec = field.mutate
					}
					source, destination, hpa := c.source.DeepCopy(), c.destination.DeepCopy(), c.hpas[0].DeepCopy()
					err := r.Run(context.Background())
					if err == nil || !strings.Contains(err.Error(), field.name) {
						t.Fatalf("post-baseline admission field was accepted or wrong refusal: %v", err)
					}
					p, err := loadPlan(path)
					if err != nil {
						t.Fatal(err)
					}
					if len(c.mutations) != 0 || !reflect.DeepEqual(c.source, source) || !reflect.DeepEqual(c.destination, destination) || !reflect.DeepEqual(c.hpas[0], *hpa) || p.Progress.Phase != "staged" {
						t.Fatal("admission field bypass retired a workload or changed its HPA", c.mutations)
					}
					assessment := p.Progress.Constraints
					if assessment == nil || assessment.Status != "blocked" || len(assessment.Checks) == 0 {
						t.Fatal("missing blocked assessment")
					}
					blocked := assessment.Checks[len(assessment.Checks)-1]
					if blocked.Name != expectedCheck || blocked.Status != "blocked" {
						t.Fatalf("wrong constraint stage: %+v", blocked)
					}
				})
			}
		}
	}
}

func TestBaselineSCCAndPodDefaultsRemainUsable(t *testing.T) {
	r, c, path := cutoverTest(t, "exclusive", true)
	c.mutateSCCSpec = func(spec map[string]interface{}) {
		spec["hostUsers"] = false
		spec["securityContext"] = map[string]interface{}{"runAsUser": int64(1000000), "seLinuxOptions": map[string]interface{}{"level": "s0:c1,c2"}}
	}
	c.mutatePodSpec = func(spec map[string]interface{}) { spec["enableServiceLinks"] = false }
	r.Options.Mode = "preflight"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal("baseline defaults were incorrectly blocked", err)
	}
	p, err := loadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Progress.Constraints.Status != "passed" || p.Progress.Phase != "staged" || len(c.mutations) != 0 {
		t.Fatal("read-only baseline preflight did not pass cleanly")
	}
}
