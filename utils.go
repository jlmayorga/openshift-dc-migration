package main

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

func hasTriggers(dc *unstructured.Unstructured) bool {
	triggers, _, _ := unstructured.NestedSlice(dc.Object, "spec", "triggers")
	return len(triggers) > 0
}

func hasLifecycleHooks(dc *unstructured.Unstructured) bool {
	_, preHookFound, _ := unstructured.NestedMap(dc.Object, "spec", "strategy", "recreateParams", "pre")
	_, midHookFound, _ := unstructured.NestedMap(dc.Object, "spec", "strategy", "recreateParams", "mid")
	_, postHookFound, _ := unstructured.NestedMap(dc.Object, "spec", "strategy", "recreateParams", "post")
	return preHookFound || midHookFound || postHookFound
}

func hasAutoRollbacks(dc *unstructured.Unstructured) bool {
	autoRollback, _, _ := unstructured.NestedBool(dc.Object, "spec", "strategy", "rollingParams", "autoRollbackEnabled")
	return autoRollback
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
