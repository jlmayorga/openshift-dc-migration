package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
)

func nodeTermsMatch(terms []corev1.NodeSelectorTerm, node corev1.Node) bool {
	for _, term := range terms {
		if len(term.MatchExpressions)+len(term.MatchFields) == 0 {
			continue
		}
		match := true
		for _, pair := range []struct {
			rules  []corev1.NodeSelectorRequirement
			values labels.Set
		}{{term.MatchExpressions, labels.Set(node.Labels)}, {term.MatchFields, labels.Set{"metadata.name": node.Name}}} {
			for _, rule := range pair.rules {
				var operator selection.Operator
				switch rule.Operator {
				case corev1.NodeSelectorOpIn:
					operator = selection.In
				case corev1.NodeSelectorOpNotIn:
					operator = selection.NotIn
				case corev1.NodeSelectorOpExists:
					operator = selection.Exists
				case corev1.NodeSelectorOpDoesNotExist:
					operator = selection.DoesNotExist
				case corev1.NodeSelectorOpGt:
					operator = selection.GreaterThan
				case corev1.NodeSelectorOpLt:
					operator = selection.LessThan
				default:
					match = false
					continue
				}
				requirement, err := labels.NewRequirement(rule.Key, operator, rule.Values)
				if err != nil || !requirement.Matches(pair.values) {
					match = false
				}
			}
		}
		if match {
			return true
		}
	}
	return false
}
func nodeEligible(node corev1.Node, pod *corev1.Pod) bool {
	if node.Spec.Unschedulable || node.DeletionTimestamp != nil {
		return false
	}
	ready := false
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			ready = condition.Status == corev1.ConditionTrue
		}
		if (condition.Type == corev1.NodeMemoryPressure || condition.Type == corev1.NodeDiskPressure || condition.Type == corev1.NodePIDPressure) && condition.Status == corev1.ConditionTrue {
			return false
		}
	}
	if !ready || pod.Spec.NodeName != "" && pod.Spec.NodeName != node.Name || !labels.SelectorFromSet(pod.Spec.NodeSelector).Matches(labels.Set(node.Labels)) {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		tolerated := false
		for _, t := range pod.Spec.Tolerations {
			if t.ToleratesTaint(&taint) && (taint.Effect != corev1.TaintEffectNoExecute || t.TolerationSeconds == nil) {
				tolerated = true
			}
		}
		if !tolerated {
			return false
		}
	}
	if pod.Spec.Affinity != nil && pod.Spec.Affinity.NodeAffinity != nil && pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		if !nodeTermsMatch(pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms, node) {
			return false
		}
	}
	return true
}
func hostPortsConflict(a, b *corev1.Pod) bool {
	for _, ca := range a.Spec.Containers {
		for _, pa := range ca.Ports {
			if pa.HostPort == 0 {
				continue
			}
			for _, cb := range b.Spec.Containers {
				for _, pb := range cb.Ports {
					if pb.HostPort == 0 {
						continue
					}
					aproto, bproto := pa.Protocol, pb.Protocol
					if aproto == "" {
						aproto = corev1.ProtocolTCP
					}
					if bproto == "" {
						bproto = corev1.ProtocolTCP
					}
					if pa.HostPort == pb.HostPort && aproto == bproto && (pa.HostIP == "" || pa.HostIP == "0.0.0.0" || pa.HostIP == "::" || pb.HostIP == "" || pb.HostIP == "0.0.0.0" || pb.HostIP == "::" || pa.HostIP == pb.HostIP) {
						return true
					}
				}
			}
		}
	}
	return false
}
func (r *Runner) checkScheduling(ctx context.Context, pod *corev1.Pod, replicas int32) error {
	if pod.Spec.HostNetwork || pod.Spec.HostPID || pod.Spec.HostIPC {
		return fmt.Errorf("host namespaces require manual placement/resource review before source retirement")
	}
	if pod.Spec.SchedulerName != "" && pod.Spec.SchedulerName != "default-scheduler" {
		return fmt.Errorf("custom scheduler %s cannot be assessed; manual scheduling review required", pod.Spec.SchedulerName)
	}
	if len(pod.Spec.SchedulingGates) > 0 || len(pod.Spec.ResourceClaims) > 0 {
		return fmt.Errorf("scheduling gates or dynamic resource claims require manual review before source retirement")
	}
	if pod.Spec.Affinity != nil {
		a := pod.Spec.Affinity
		if a.PodAffinity != nil && len(a.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution) > 0 || a.PodAntiAffinity != nil && len(a.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) > 0 {
			return fmt.Errorf("required inter-pod affinity/anti-affinity is not simulated; manual migration required")
		}
	}
	for _, spread := range pod.Spec.TopologySpreadConstraints {
		if spread.WhenUnsatisfiable == corev1.DoNotSchedule {
			return fmt.Errorf("hard topology-spread constraints require manual scheduling review")
		}
	}
	var volumeAffinity []*corev1.NodeSelector
	for _, volume := range pod.Spec.Volumes {
		if volume.Ephemeral != nil {
			return fmt.Errorf("ephemeral PVC provisioning cannot be proven before source retirement")
		}
		if volume.PersistentVolumeClaim != nil {
			claim, err := r.Kube.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(ctx, volume.PersistentVolumeClaim.ClaimName, metav1.GetOptions{})
			if err != nil {
				return err
			}
			pv, err := r.Kube.CoreV1().PersistentVolumes().Get(ctx, claim.Spec.VolumeName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("read PVC node affinity: %w", err)
			}
			if err := checkLegacyPV(pv); err != nil {
				return fmt.Errorf("PVC %q: %w", claim.Name, err)
			}
			if pv.Spec.NodeAffinity != nil && pv.Spec.NodeAffinity.Required != nil {
				volumeAffinity = append(volumeAffinity, pv.Spec.NodeAffinity.Required)
			}
		}
	}
	var nodes []corev1.Node
	options := metav1.ListOptions{Limit: 500}
	for {
		list, err := r.Kube.CoreV1().Nodes().List(ctx, options)
		if err != nil {
			return fmt.Errorf("cannot verify nodes: %w", err)
		}
		nodes = append(nodes, list.Items...)
		if list.GetContinue() == "" {
			break
		}
		options.Continue = list.GetContinue()
	}
	unknown := map[string]bool{}
	allocated := map[string]corev1.ResourceList{}
	bound := map[string][]corev1.Pod{}
	options = metav1.ListOptions{Limit: 500}
	for {
		list, err := r.Dynamic.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).List(ctx, options)
		if err != nil {
			return fmt.Errorf("cannot verify current cluster allocations: %w", err)
		}
		for _, raw := range list.Items {
			var current corev1.Pod
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &current); err != nil {
				return err
			}

			if current.Spec.NodeName == "" || current.Status.Phase == corev1.PodSucceeded || current.Status.Phase == corev1.PodFailed {
				continue
			}

			name := current.Spec.NodeName
			if allocated[name] == nil {
				allocated[name] = corev1.ResourceList{}
			}
			used, accountingErr := observedPodRequests(&raw)
			if accountingErr != nil {
				unknown[name] = true
				continue
			}
			used[corev1.ResourcePods] = resource.MustParse("1")
			for key, q := range used {
				v := allocated[name][key]
				v.Add(q)
				allocated[name][key] = v
			}
			bound[name] = append(bound[name], current)
		}
		if list.GetContinue() == "" {
			break
		}
		options.Continue = list.GetContinue()
	}
	needed := podResources(pod, false)
	needed[corev1.ResourcePods] = resource.MustParse("1")

	if replicas > 100000 {
		return fmt.Errorf("replica count %s exceeds bounded capacity assessment; manual review required", strconv.Itoa(int(replicas)))
	}
	remaining := int64(replicas)
	for _, node := range nodes {
		if unknown[node.Name] || !nodeEligible(node, pod) {
			continue
		}
		valid := true
		for _, affinity := range volumeAffinity {
			if !nodeTermsMatch(affinity.NodeSelectorTerms, node) {
				valid = false
			}
		}
		if !valid {
			continue
		}
		for _, current := range bound[node.Name] {
			if hostPortsConflict(pod, &current) {
				valid = false
			}
		}
		if !valid {
			continue
		}
		available := node.Status.Allocatable.DeepCopy()
		for key, q := range allocated[node.Name] {
			v := available[key]
			v.Sub(q)
			available[key] = v
		}
		for remaining > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			fits := true
			for key, q := range needed {
				if v := available[key]; v.Cmp(q) < 0 {
					fits = false
					break
				}
			}
			if !fits {
				break
			}
			for key, q := range needed {
				v := available[key]
				v.Sub(q)
				available[key] = v
			}
			remaining--
			// Multiple destination replicas cannot share a requested host port.
			if hostPortsConflict(pod, pod) {
				break
			}
		}
		if remaining == 0 {
			return nil
		}
	}
	return fmt.Errorf("no verified node capacity/placement for %d of %d additional destination replicas while retaining source; inspect Ready/schedulable nodes, selectors, taints, host ports, PVC topology and CPU/memory/pod requests (unsupported observed allocations on %d nodes)", remaining, replicas, len(unknown))
}

// Read newer allocation fields without raising the 1.27 output baseline.
// Maxima during in-place resize conservatively retain already allocated capacity.
func observedPodRequests(raw *unstructured.Unstructured) (corev1.ResourceList, error) {
	var pod corev1.Pod
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &pod); err != nil {
		return nil, err
	}
	decode := func(obj map[string]interface{}) (corev1.ResourceList, error) {
		var requirements corev1.ResourceRequirements
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(map[string]interface{}{"requests": obj}, &requirements)
		return requirements.Requests, err
	}
	for _, field := range []string{"containerStatuses", "initContainerStatuses"} {
		statuses, _, err := unstructured.NestedSlice(raw.Object, "status", field)
		if err != nil {
			return nil, err
		}
		for _, value := range statuses {
			status, ok := value.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("malformed allocation status")
			}
			name, _ := status["name"].(string)
			for _, path := range [][]string{{"allocatedResources"}, {"resources", "requests"}} {
				values, found, e := unstructured.NestedMap(status, path...)
				if e != nil {
					return nil, e
				}
				if !found {
					continue
				}
				requests, e := decode(values)
				if e != nil {
					return nil, e
				}
				containers := pod.Spec.Containers
				if field == "initContainerStatuses" {
					containers = pod.Spec.InitContainers
				}
				for i := range containers {
					if containers[i].Name != name {
						continue
					}
					if containers[i].Resources.Requests == nil {
						containers[i].Resources.Requests = corev1.ResourceList{}
					}
					for resourceName, q := range requests {
						if old := containers[i].Resources.Requests[resourceName]; old.Cmp(q) < 0 {
							containers[i].Resources.Requests[resourceName] = q.DeepCopy()
						}
					}
				}
			}
		}
	}
	used := podResources(&pod, false)
	values, found, err := unstructured.NestedMap(raw.Object, "spec", "resources", "requests")
	if err != nil {
		return nil, err
	}
	if found {
		requests, err := decode(values)
		if err != nil {
			return nil, err
		}
		for name, q := range requests {
			if name != corev1.ResourceCPU && name != corev1.ResourceMemory && !strings.HasPrefix(string(name), "hugepages-") {
				return nil, fmt.Errorf("unknown Pod budget resource %s", name)
			}
			value := q.DeepCopy()
			value.Add(pod.Spec.Overhead[name])
			used[name] = value
		}
	}
	return used, nil
}
