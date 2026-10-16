package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const adminPolicyGroup = "policy.networking.k8s.io"
const adminPolicyVersion = "v1alpha1"

// Optional policy APIs are discovered independently from baseline workload APIs.
// Absence is safe; denied discovery and unknown policy APIs require review.
func (r *Runner) adminPolicyResources(ctx context.Context) ([]schema.GroupVersionResource, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Options.RequestTimeout)
	defer cancel()
	var group metav1.APIGroup
	err := r.Kube.Discovery().RESTClient().Get().AbsPath("/apis/" + adminPolicyGroup).Do(ctx).Into(&group)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("discover cluster admin network policies: %w", err)
	}
	if group.Name != adminPolicyGroup || len(group.Versions) == 0 {
		return nil, errors.New("admin network policy discovery omitted a valid API group/version; manual review required")
	}
	for _, version := range group.Versions {
		if version.GroupVersion != adminPolicyGroup+"/"+adminPolicyVersion {
			return nil, fmt.Errorf("admin network policy API %s needs manual assessment before source retirement", version.GroupVersion)
		}
	}
	resources, err := r.discover(ctx, adminPolicyGroup+"/"+adminPolicyVersion)
	if err != nil {
		return nil, err
	}
	if resources.GroupVersion != adminPolicyGroup+"/"+adminPolicyVersion {
		return nil, errors.New("admin network policy resource discovery returned an unexpected version")
	}
	var result []schema.GroupVersionResource
	for _, resource := range resources.APIResources {
		if strings.Contains(resource.Name, "/") {
			continue
		}
		if resource.Namespaced || resource.Name != "adminnetworkpolicies" && resource.Name != "baselineadminnetworkpolicies" {
			return nil, fmt.Errorf("admin network policy resource %s needs manual assessment", resource.Name)
		}
		result = append(result, schema.GroupVersionResource{Group: adminPolicyGroup, Version: adminPolicyVersion, Resource: resource.Name})
	}
	if len(result) == 0 {
		return nil, errors.New("admin network policy API has no assessable resources; manual review required")
	}
	return result, nil
}

func policyFields(raw map[string]interface{}, allowed ...string) error {
	for key := range raw {
		known := false
		for _, name := range allowed {
			known = known || key == name
		}
		if !known {
			return fmt.Errorf("unassessed admin network policy field %s", key)
		}
	}
	return nil
}

func policySelector(raw interface{}) (labels.Selector, error) {
	object, ok := raw.(map[string]interface{})
	if !ok {
		return nil, errors.New("admin network policy omitted a valid selector")
	}
	if err := validateBaseline(object, "io.k8s.apimachinery.pkg.apis.meta.v1.LabelSelector"); err != nil {
		return nil, err
	}
	var selector metav1.LabelSelector
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object, &selector); err != nil {
		return nil, err
	}
	return metav1.LabelSelectorAsSelector(&selector)
}

func compareAdminSelection(raw map[string]interface{}, namespace map[string]string, sources []map[string]string, target map[string]string) error {
	if err := policyFields(raw, "namespaceSelector", "podSelector"); err != nil {
		return err
	}
	nsSelector, err := policySelector(raw["namespaceSelector"])
	if err != nil {
		return err
	}
	podSelector, err := policySelector(raw["podSelector"])
	if err != nil {
		return err
	}
	if nsSelector.Matches(labels.Set(namespace)) {
		for _, source := range sources {
			if podSelector.Matches(labels.Set(source)) != podSelector.Matches(labels.Set(target)) {
				return errors.New("selects current source Pods and proposed destination differently; review cluster workload/peer selectors before source retirement")
			}
		}
	}
	return nil
}

func assessAdminPolicy(policy unstructured.Unstructured, namespace map[string]string, sources []map[string]string, target map[string]string) error {
	spec, found, err := unstructured.NestedMap(policy.Object, "spec")
	if err != nil || !found {
		return errors.New("admin network policy omitted its spec")
	}
	if err = policyFields(spec, "priority", "subject", "ingress", "egress"); err != nil {
		return err
	}
	selection := func(value interface{}, subject bool) error {
		peer, ok := value.(map[string]interface{})
		if !ok || len(peer) != 1 {
			return errors.New("admin network policy omitted an unambiguous subject/peer")
		}
		for key, raw := range peer {
			switch key {
			case "pods":
				pods, ok := raw.(map[string]interface{})
				if !ok {
					return errors.New("invalid admin network policy Pod selection")
				}
				return compareAdminSelection(pods, namespace, sources, target)
			case "namespaces":
				_, err := policySelector(raw)
				return err // Namespace identity/labels do not change during migration.
			case "nodes", "networks", "domainNames":
				if !subject {
					return nil // External/node reachability is explicitly a runtime limit.
				}
			}
			return fmt.Errorf("unassessed admin network policy subject/peer %s", key)
		}
		return nil
	}
	if err = selection(spec["subject"], true); err != nil {
		return fmt.Errorf("subject: %w", err)
	}
	for _, direction := range []string{"ingress", "egress"} {
		if spec[direction] == nil {
			continue
		}
		rules, ok := spec[direction].([]interface{})
		if !ok {
			return fmt.Errorf("invalid admin network policy %s rules", direction)
		}
		peerKey := "from"
		if direction == "egress" {
			peerKey = "to"
		}
		for _, value := range rules {
			rule, ok := value.(map[string]interface{})
			if !ok {
				return errors.New("invalid admin network policy rule")
			}
			if err = policyFields(rule, "name", "action", peerKey, "ports"); err != nil {
				return err
			}
			if action := rule["action"]; action != "Allow" && action != "Deny" && action != "Pass" {
				return fmt.Errorf("unassessed admin network policy action %v", action)
			}
			peers, ok := rule[peerKey].([]interface{})
			if !ok || len(peers) == 0 {
				return errors.New("admin network policy omitted rule peers")
			}
			for _, peer := range peers {
				if err = selection(peer, false); err != nil {
					return fmt.Errorf("%s peer: %w", direction, err)
				}
			}
		}
	}
	return nil
}

func (r *Runner) checkAdminPolicies(ctx context.Context, p *MigrationPlan, pod *corev1.Pod, assessment *ConstraintAssessment) error {
	resources, err := r.adminPolicyResources(ctx)
	if err != nil {
		return err
	}
	var policies []unstructured.Unstructured
	for _, resource := range resources {
		if err = r.access(ctx, "", resource.Group, resource.Resource, "list"); err != nil {
			return err
		}
		options := metav1.ListOptions{Limit: 500}
		seen := map[string]bool{}
		for {
			page, err := r.Dynamic.Resource(resource).List(ctx, options)
			if err != nil {
				return fmt.Errorf("read cluster %s: %w", resource.Resource, err)
			}
			policies = append(policies, page.Items...)
			options.Continue = page.GetContinue()
			if options.Continue == "" {
				break
			}
			if seen[options.Continue] {
				return errors.New("admin network policy inventory repeated a continuation token")
			}
			seen[options.Continue] = true
		}
	}
	if len(policies) == 0 {
		return nil
	}
	ns := p.Source.GetNamespace()
	namespace, err := r.Kube.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get namespace %s labels for cluster policy assessment: %w", ns, err)
	}
	sourceSelector, found, err := unstructured.NestedStringMap(p.Source.Object, "spec", "selector")
	if err != nil || !found || len(sourceSelector) == 0 {
		return errors.New("missing source Pod selector for cluster policy assessment")
	}
	current, err := r.Kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(sourceSelector).String()})
	if err != nil {
		return err
	}
	var sources []map[string]string
	for _, source := range current.Items {
		if source.Status.Phase != corev1.PodSucceeded && source.Status.Phase != corev1.PodFailed {
			sources = append(sources, source.Labels)
		}
	}
	if len(sources) == 0 {
		return errors.New("no current source Pods to compare with cluster admin network policies")
	}
	for _, policy := range policies {
		if err = assessAdminPolicy(policy, namespace.Labels, sources, pod.Labels); err != nil {
			return fmt.Errorf("%s %s: %w", policy.GetKind(), policy.GetName(), err)
		}
	}
	assessment.Warnings = append(assessment.Warnings, "Cluster admin network policy subject/peer Pod selectors match current source and destination equivalently. Rule priority, ports, node/IP/domain reachability and actual connectivity still require runtime review; this is not a network-policy engine.")
	return nil
}
