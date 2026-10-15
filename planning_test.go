package main

import "testing"

func TestHPABaselineRejectsNewSemantics(t *testing.T) {
	spec := map[string]interface{}{"scaleTargetRef": map[string]interface{}{"apiVersion": "apps.openshift.io/v1", "kind": "DeploymentConfig", "name": "web"}, "maxReplicas": int64(3)}
	if err := validateBaseline(spec, "io.k8s.api.autoscaling.v2.HorizontalPodAutoscalerSpec"); err != nil {
		t.Fatal("baseline HPA not accepted", err)
	}
	spec["behavior"] = map[string]interface{}{"scaleUp": map[string]interface{}{"tolerance": "0.1"}}
	if validateBaseline(spec, "io.k8s.api.autoscaling.v2.HorizontalPodAutoscalerSpec") == nil {
		t.Fatal("newer HPA field could be lost on recreation")
	}
}
