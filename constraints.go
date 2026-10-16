package main

import (
	"context"
	"errors"
	"fmt"
	"k8s.io/apimachinery/pkg/util/intstr"
	"math"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type ConstraintCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type ConstraintAssessment struct {
	DesiredReplicas  int32             `json:"desiredReplicas"`
	CapacityReplicas int32             `json:"capacityReplicas"`
	CheckedAt        string            `json:"checkedAt"`
	Status           string            `json:"status"`
	Checks           []ConstraintCheck `json:"checks"`
	Blockers         []string          `json:"blockers,omitempty"`
	Warnings         []string          `json:"warnings"`
}

func (r *Runner) assessConstraints(ctx context.Context, p *MigrationPlan, staged *appsv1.Deployment, result *Result) (runErr error) {
	a := &ConstraintAssessment{CheckedAt: r.Now().UTC().Format("2006-01-02T15:04:05Z"), Status: "blocked", Checks: []ConstraintCheck{}, Warnings: []string{
		"Point-in-time checks do not reserve quota or node capacity. Concurrent changes, image/runtime/storage failures and controller-identity or generated-name-specific admission can still prevent startup.",
		"Quota and scheduling checks require headroom for destination replicas plus configured rolling surge while retaining the source; source retirement is never credited as available capacity.",
		"Scheduler plugins, CSI attachment limits, volume mounting and external network dependencies are not simulated; review these before exclusive cutover.",
	}}
	result.Constraints = a
	p.Progress.Constraints = a
	defer func() {
		result.Warnings = append(result.Warnings, a.Warnings...)
		if runErr == nil {
			p.Progress.Error = ""
		}
		if runErr != nil {
			a.Blockers = append(a.Blockers, runErr.Error())
		}
		if err := savePlan(r.Options.PlanFile, p); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	check := func(name string, fn func() error) error {
		err := fn()
		status, detail := "passed", "verified before source retirement"
		if err != nil {
			status = "blocked"
			detail = err.Error()
		}
		a.Checks = append(a.Checks, ConstraintCheck{Name: name, Status: status, Detail: detail})
		if err != nil {
			return fmt.Errorf("pre-cutover %s: %w", name, err)
		}
		return nil
	}
	replicas := *p.Desired.Spec.Replicas
	capacity := replicas
	if replicas > 0 && p.Desired.Spec.Strategy.Type == appsv1.RollingUpdateDeploymentStrategyType && p.Desired.Spec.Strategy.RollingUpdate != nil && p.Desired.Spec.Strategy.RollingUpdate.MaxSurge != nil {
		surge, err := intstr.GetScaledValueFromIntOrPercent(p.Desired.Spec.Strategy.RollingUpdate.MaxSurge, int(replicas), true)
		if err != nil {
			return err
		}
		if surge < 0 || surge > math.MaxInt32 {
			return errors.New("rolling surge exceeds bounded preflight capacity")
		}
		surgeReplicas := int32(surge)
		if replicas > math.MaxInt32-surgeReplicas {
			return errors.New("destination plus surge exceeds bounded preflight capacity")
		}
		capacity += surgeReplicas
	}
	a.DesiredReplicas = replicas
	a.CapacityReplicas = capacity

	ns := p.Source.GetNamespace()
	if err := check("permissions", func() error {
		perms := [][4]string{{ns, "apps", "deployments", "update"}, {ns, "apps.openshift.io", "deploymentconfigs", "update"}}
		if replicas > 0 {
			perms = append(perms, [][4]string{{ns, "networking.k8s.io", "networkpolicies", "list"}, {ns, "", "pods", "create"}, {ns, "apps", "replicasets", "create"}, {ns, "", "resourcequotas", "list"}, {ns, "", "limitranges", "list"}, {ns, "security.openshift.io", "podsecuritypolicysubjectreviews", "create"}, {ns, "quota.openshift.io", "appliedclusterresourcequotas", "list"}, {"", "", "nodes", "list"}, {"", "", "pods", "list"}}...)
		}
		if len(p.HPAs) > 0 {
			perms = append(perms, [][4]string{{ns, "autoscaling", "horizontalpodautoscalers", "create"}, {ns, "autoscaling", "horizontalpodautoscalers", "delete"}}...)
		}
		for _, perm := range perms {
			if err := r.access(ctx, perm[0], perm[1], perm[2], perm[3]); err != nil {
				return fmt.Errorf("cannot verify constraints; grant required read/dry-run permission: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	active := staged.DeepCopy()
	active.Spec.Paused = false
	active.Spec.Replicas = &replicas
	if tr := p.Desired.Annotations[imageTriggerAnnotation]; tr != "" {
		active.Annotations[imageTriggerAnnotation] = tr
	}
	if err := check("deployment-admission", func() error {
		admitted, err := r.Kube.AppsV1().Deployments(ns).Update(ctx, active, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(active.Spec, admitted.Spec) || active.Annotations[imageTriggerAnnotation] != admitted.Annotations[imageTriggerAnnotation] {
			return errors.New("admission changed planned activation; review before cutover")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := check("source-admission", func() error {
		for _, spec := range []map[string]interface{}{sourceSpec(p, false, false), sourceSpec(p, true, false), parkedSourceSpec(p)} {
			request := p.Source.DeepCopy()
			_ = unstructured.SetNestedMap(request.Object, spec, "spec")
			admitted, err := r.Dynamic.Resource(dcResource).Namespace(ns).Update(ctx, request, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(admitted.Object["spec"], spec) {
				return errors.New("admission changed source retirement semantics")
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := check("autoscaler-admission", func() error {
		for i, h := range p.HPAs {
			current, err := r.Kube.AutoscalingV2().HorizontalPodAutoscalers(ns).Get(ctx, h.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if current.UID != h.UID || !reflect.DeepEqual(current.Spec, h.Spec) {
				return fmt.Errorf("HPA %s changed before preflight", h.Name)
			}
			uid, rv := current.UID, current.ResourceVersion
			if err = r.Kube.AutoscalingV2().HorizontalPodAutoscalers(ns).Delete(ctx, h.Name, metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}, Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}); err != nil {
				return err
			}
			request := h.DeepCopy()
			request.ObjectMeta = metav1.ObjectMeta{Name: fmt.Sprintf("dcmig-check-%s-%d", p.ID, i), Namespace: ns}
			request.Status = autoscalingv2.HorizontalPodAutoscalerStatus{}
			request.Spec.ScaleTargetRef.APIVersion = "apps/v1"
			request.Spec.ScaleTargetRef.Kind = "Deployment"
			admitted, err := r.Kube.AutoscalingV2().HorizontalPodAutoscalers(ns).Create(ctx, request, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(admitted.Spec, request.Spec) {
				return fmt.Errorf("HPA admission changed retargeted semantics")
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if replicas == 0 {
		a.Status = "passed"
		a.Warnings = append(a.Warnings, "Zero replicas requested: no Pod or ReplicaSet startup is assessed; later scaling requires fresh checks.")
		return nil
	}
	controller := true
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "dcmig-check-" + p.ID, Namespace: ns, Labels: active.Spec.Template.Labels, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: active.Name, UID: active.UID, Controller: &controller}}}, Spec: appsv1.ReplicaSetSpec{Replicas: &replicas, MinReadySeconds: active.Spec.MinReadySeconds, Selector: active.Spec.Selector.DeepCopy(), Template: *active.Spec.Template.DeepCopy()}}
	if err := check("replicaset-admission", func() error {
		admitted, err := r.Kube.AppsV1().ReplicaSets(ns).Create(ctx, rs, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(admitted.Spec, rs.Spec) {
			return errors.New("admission changed destination ReplicaSet; manual review required")
		}
		return nil
	}); err != nil {
		return err
	}
	template := active.Spec.Template.DeepCopy()
	if err := check("service-account-scc", func() error {
		if err := r.requireAPI(ctx, "security.openshift.io/v1", "podsecuritypolicysubjectreviews"); err != nil {
			return err
		}
		sa := template.Spec.ServiceAccountName
		if sa == "" {
			sa = "default"
		}
		template.Spec.ServiceAccountName = sa
		obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(template)
		if err != nil {
			return err
		}
		review := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "security.openshift.io/v1", "kind": "PodSecurityPolicySubjectReview", "spec": map[string]interface{}{"template": obj, "user": "system:serviceaccount:" + ns + ":" + sa, "groups": []interface{}{"system:serviceaccounts", "system:serviceaccounts:" + ns, "system:authenticated"}}}}
		response, err := r.Dynamic.Resource(schema.GroupVersionResource{Group: "security.openshift.io", Version: "v1", Resource: "podsecuritypolicysubjectreviews"}).Namespace(ns).Create(ctx, review, metav1.CreateOptions{FieldValidation: "Strict"})
		if err != nil {
			return err
		}
		allowed, found, _ := unstructured.NestedMap(response.Object, "status", "allowedBy")
		if !found || len(allowed) == 0 {
			reason, _, _ := unstructured.NestedString(response.Object, "status", "reason")
			return fmt.Errorf("service account %s cannot admit destination SCC template: %s", sa, reason)
		}
		spec, found, e := unstructured.NestedMap(response.Object, "status", "template", "spec")
		if e != nil || !found {
			return errors.New("SCC review omitted its defaulted Pod spec")
		}
		// Subject reviews can omit template metadata. Preserve the exact
		// destination labels/annotations for policy evaluation and routing.
		return runtime.DefaultUnstructuredConverter.FromUnstructured(spec, &template.Spec)
	}); err != nil {
		return err
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "dcmig-check-" + p.ID, Namespace: ns, Labels: template.Labels, Annotations: template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: types.UID("dry-run-" + p.ID), Controller: &controller}}}, Spec: *template.Spec.DeepCopy()}
	if err := check("pod-admission-and-limits", func() error {
		if _, err := r.Kube.CoreV1().LimitRanges(ns).List(ctx, metav1.ListOptions{}); err != nil {
			return err
		}
		admitted, err := r.Kube.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
		if err != nil {
			return err
		}
		pod = admitted
		return nil
	}); err != nil {
		return err
	}
	if err := check("namespace-and-cluster-quotas", func() error { return r.checkQuotas(ctx, ns, pod, capacity) }); err != nil {
		return err
	}
	if err := check("network-policy-selection", func() error {
		policies, err := r.Kube.NetworkingV1().NetworkPolicies(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		sourceLabels, _, err := unstructured.NestedStringMap(p.Source.Object, "spec", "template", "metadata", "labels")
		if err != nil {
			return err
		}
		for _, policy := range policies.Items {
			selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
			if err != nil {
				return err
			}
			if selector.Matches(labels.Set(sourceLabels)) != selector.Matches(labels.Set(pod.Labels)) {
				return fmt.Errorf("NetworkPolicy %s selects source and destination differently; review connectivity before source retirement", policy.Name)
			}
		}
		if len(policies.Items) > 0 {
			a.Warnings = append(a.Warnings, "Namespace NetworkPolicies were compared by workload selector; peer selectors, external network rules and actual destination connectivity remain runtime checks.")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := check("storage-references", func() error {
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil {
				claim, err := r.Kube.CoreV1().PersistentVolumeClaims(ns).Get(ctx, volume.PersistentVolumeClaim.ClaimName, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if claim.DeletionTimestamp != nil || claim.Status.Phase != corev1.ClaimBound {
					return fmt.Errorf("PVC %s is deleting or not Bound; resolve storage before source retirement", claim.Name)
				}
				a.Warnings = append(a.Warnings, "PVC "+claim.Name+": attachment, mount and node-failure behavior are not proven by API dry run.")
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := check("cluster-scheduling", func() error { return r.checkScheduling(ctx, pod, capacity) }); err != nil {
		return err
	}
	a.Status = "passed"
	return nil
}
