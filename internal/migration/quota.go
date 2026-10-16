package migration

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func podResources(pod *corev1.Pod, limits bool) corev1.ResourceList {
	total, sidecars, peak := corev1.ResourceList{}, corev1.ResourceList{}, corev1.ResourceList{}
	add := func(dst corev1.ResourceList, src corev1.ResourceList) {
		for name, q := range src {
			v := dst[name]
			v.Add(q)
			dst[name] = v
		}
	}
	maximum := func(dst corev1.ResourceList, src corev1.ResourceList) {
		for name, q := range src {
			if v := dst[name]; v.Cmp(q) < 0 {
				dst[name] = q.DeepCopy()
			}
		}
	}
	resources := func(c corev1.Container) corev1.ResourceList {
		if limits {
			return c.Resources.Limits
		}
		return c.Resources.Requests
	}
	for _, c := range pod.Spec.Containers {
		add(total, resources(c))
	}
	for _, c := range pod.Spec.InitContainers {
		current := sidecars.DeepCopy()
		if c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			add(sidecars, resources(c))
			current = sidecars.DeepCopy()
		} else {
			add(current, resources(c))
		}
		maximum(peak, current)
	}
	add(total, sidecars)
	maximum(total, peak)
	for name, q := range pod.Spec.Overhead {
		v, found := total[name]
		if !limits || found && !v.IsZero() {
			v.Add(q)
			total[name] = v
		}
	}
	return total
}

func quotaApplies(spec corev1.ResourceQuotaSpec, pod *corev1.Pod) (bool, error) {
	bestEffort := true
	for _, c := range append(append([]corev1.Container{}, pod.Spec.Containers...), pod.Spec.InitContainers...) {
		for _, r := range []corev1.ResourceList{c.Resources.Requests, c.Resources.Limits} {
			for _, n := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
				if q := r[n]; !q.IsZero() {
					bestEffort = false
				}
			}
		}
	}
	matches := func(scope corev1.ResourceQuotaScope) (bool, error) {
		switch string(scope) {
		case "Terminating":
			return pod.Spec.ActiveDeadlineSeconds != nil, nil
		case "NotTerminating":
			return pod.Spec.ActiveDeadlineSeconds == nil, nil
		case "BestEffort":
			return bestEffort, nil
		case "NotBestEffort":
			return !bestEffort, nil
		case "PriorityClass":
			return pod.Spec.PriorityClassName != "", nil
		case "CrossNamespacePodAffinity":
			if pod.Spec.Affinity != nil {
				for _, aff := range []*corev1.PodAffinity{pod.Spec.Affinity.PodAffinity} {
					if aff != nil {
						for _, term := range aff.RequiredDuringSchedulingIgnoredDuringExecution {
							if len(term.Namespaces) > 0 || term.NamespaceSelector != nil {
								return true, nil
							}
						}
						for _, term := range aff.PreferredDuringSchedulingIgnoredDuringExecution {
							if len(term.PodAffinityTerm.Namespaces) > 0 || term.PodAffinityTerm.NamespaceSelector != nil {
								return true, nil
							}
						}
					}
				}
				if aff := pod.Spec.Affinity.PodAntiAffinity; aff != nil {
					for _, term := range aff.RequiredDuringSchedulingIgnoredDuringExecution {
						if len(term.Namespaces) > 0 || term.NamespaceSelector != nil {
							return true, nil
						}
					}
					for _, term := range aff.PreferredDuringSchedulingIgnoredDuringExecution {
						if len(term.PodAffinityTerm.Namespaces) > 0 || term.PodAffinityTerm.NamespaceSelector != nil {
							return true, nil
						}
					}
				}
			}
			return false, nil
		}
		return false, fmt.Errorf("quota scope %s cannot be evaluated safely", scope)
	}
	for _, scope := range spec.Scopes {
		match, err := matches(scope)
		if err != nil || !match {
			return match, err
		}
	}
	if spec.ScopeSelector != nil {
		for _, req := range spec.ScopeSelector.MatchExpressions {
			match, err := matches(req.ScopeName)
			if err != nil {
				return false, err
			}
			switch string(req.Operator) {
			case "Exists":
			case "DoesNotExist":
				match = !match
			case "In", "NotIn":
				if req.ScopeName != corev1.ResourceQuotaScopePriorityClass {
					return false, fmt.Errorf("cannot evaluate value selector for scope %s", req.ScopeName)
				}
				match = false
				for _, value := range req.Values {
					if value == pod.Spec.PriorityClassName {
						match = true
					}
				}
				if string(req.Operator) == "NotIn" {
					match = !match
				}
			default:
				return false, fmt.Errorf("unknown quota selector %s", req.Operator)
			}
			if !match {
				return false, nil
			}
		}
	}
	return true, nil
}

func quotaHeadroom(name string, spec corev1.ResourceQuotaSpec, status corev1.ResourceQuotaStatus, pod *corev1.Pod, replicas int32) error {
	applies, err := quotaApplies(spec, pod)
	if err != nil {
		return err
	}
	requests, limits := podResources(pod, false), podResources(pod, true)
	demands := corev1.ResourceList{"count/replicasets.apps": resource.MustParse("1")}
	if applies {
		demands["pods"] = *resource.NewQuantity(int64(replicas), resource.DecimalSI)
		demands["count/pods"] = *resource.NewQuantity(int64(replicas), resource.DecimalSI)
		for n, q := range requests {
			q = q.DeepCopy()
			if !q.Mul(int64(replicas)) {
				return fmt.Errorf("quota demand overflow for %s", n)
			}
			demands[corev1.ResourceName("requests."+string(n))] = q
			demands[n] = q
		}
		for n, q := range limits {
			q = q.DeepCopy()
			if !q.Mul(int64(replicas)) {
				return fmt.Errorf("quota demand overflow for %s", n)
			}
			demands[corev1.ResourceName("limits."+string(n))] = q
		}
	}
	for key, hard := range spec.Hard {
		demand := demands[key]
		if demand.IsZero() {
			continue
		}
		observed, exists := status.Hard[key]
		used, known := status.Used[key]
		if !exists || !known || hard.Cmp(observed) != 0 {
			return fmt.Errorf("quota %s accounting for %s is not synchronized; wait for status.hard/used before cutover", name, key)
		}
		projected := used.DeepCopy()
		projected.Add(demand)
		if projected.Cmp(hard) > 0 {
			return fmt.Errorf("quota %s: %s used=%s additional=%s hard=%s; insufficient full destination headroom while retaining source; request temporary capacity or use a reviewed maintenance procedure", name, key, used.String(), demand.String(), hard.String())
		}
	}
	return nil
}

func (r *Runner) checkQuotas(ctx context.Context, ns string, pod *corev1.Pod, replicas int32) error {
	quotas, err := r.Kube.CoreV1().ResourceQuotas(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, q := range quotas.Items {
		if err := quotaHeadroom(ns+"/"+q.Name, q.Spec, q.Status, pod, replicas); err != nil {
			return err
		}
	}
	if err := r.requireAPI(ctx, "quota.openshift.io/v1", "appliedclusterresourcequotas"); err != nil {
		return err
	}
	list, err := r.Dynamic.Resource(schema.GroupVersionResource{Group: "quota.openshift.io", Version: "v1", Resource: "appliedclusterresourcequotas"}).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, q := range list.Items {
		spec, found, e := unstructured.NestedMap(q.Object, "spec", "quota")
		if e != nil || !found {
			return fmt.Errorf("cluster quota %s omitted spec.quota", q.GetName())
		}
		status, found, e := unstructured.NestedMap(q.Object, "status", "total")
		if e != nil || !found {
			return fmt.Errorf("cluster quota %s omitted total accounting", q.GetName())
		}
		var typed corev1.ResourceQuota
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(spec, &typed.Spec); err != nil {
			return err
		}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(status, &typed.Status); err != nil {
			return err
		}
		if err = quotaHeadroom("cluster/"+q.GetName(), typed.Spec, typed.Status, pod, replicas); err != nil {
			return err
		}
	}
	return nil
}
