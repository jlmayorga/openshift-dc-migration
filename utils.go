package main

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

func hasTriggers(dc *unstructured.Unstructured) bool {
	triggers, _, _ := unstructured.NestedSlice(dc.Object, "spec", "triggers")
	return len(triggers) > 0
}

func hasLifecycleHooks(dc *unstructured.Unstructured) bool {
	for _, params := range []string{"rollingParams", "recreateParams"} {
		for _, hook := range []string{"pre", "mid", "post"} {
			_, found, _ := unstructured.NestedFieldNoCopy(dc.Object, "spec", "strategy", params, hook)
			if found {
				return true
			}
		}
	}
	return false
}

func usesCustomStrategies(dc *unstructured.Unstructured) bool {
	strategyType, _, _ := unstructured.NestedString(dc.Object, "spec", "strategy", "type")
	return strategyType == "Custom"
}

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
