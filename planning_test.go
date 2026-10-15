package main

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
)

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

func TestOrphanReplicaSetBlocksStaging(t *testing.T) {
	r, c, _ := stageTest(t)
	c.extraReplicaSet = &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "apps/v1", "kind": "ReplicaSet", "metadata": map[string]interface{}{"name": "old-migration", "namespace": "example", "labels": map[string]interface{}{migrationLabel: migrationID(c.source)}}, "spec": map[string]interface{}{"replicas": int64(1), "selector": map[string]interface{}{"matchLabels": map[string]interface{}{migrationLabel: migrationID(c.source), "pod-template-hash": "old"}}}}}
	if r.Run(context.Background()) == nil || c.creates != 0 {
		t.Fatal("orphaned migration ReplicaSet was silently adopted/ignored")
	}
}
func TestForeignControllerCannotClaimSourceAnnotations(t *testing.T) {
	dc := fixture(t)
	obj := &unstructured.Unstructured{}
	obj.SetAnnotations(map[string]string{"openshift.io/deployment-config.name": dc.GetName()})
	controller := true
	obj.SetOwnerReferences([]metav1.OwnerReference{{Kind: "DeploymentConfig", UID: "foreign", Controller: &controller}})
	if sourceController(obj, dc) {
		t.Fatal("foreign controller ownership ignored")
	}
}
