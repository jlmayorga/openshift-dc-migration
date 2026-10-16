package main

import (
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestTolerationOperatorsRetainTheMinimumServerContract(t *testing.T) {
	for _, op := range []string{"", "Equal", "Exists", "Gt", "Lt", "future-operator"} {
		t.Run(op, func(t *testing.T) {
			source := fixture(t)
			value := "2"
			if op == "Exists" {
				value = ""
			}
			err := unstructured.SetNestedSlice(source.Object, []interface{}{map[string]interface{}{"key": "generation", "operator": op, "value": value, "effect": "NoSchedule"}}, "spec", "template", "spec", "tolerations")
			if err != nil {
				t.Fatal(err)
			}
			before := source.DeepCopy()
			_, err = NewRunner(DefaultOptions()).Convert(source)
			unsupported := op == "Gt" || op == "Lt" || op == "future-operator"
			if unsupported && (err == nil || !strings.Contains(err.Error(), "1.27 baseline")) {
				t.Fatalf("post-floor toleration entered baseline output: %v", err)
			}
			if !unsupported && err != nil {
				t.Fatal("baseline toleration rejected", err)
			}
			if !reflect.DeepEqual(source, before) {
				t.Fatal("source mutated")
			}
		})
	}
}
