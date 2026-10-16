package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type MigrationPlan struct {
	SchemaVersion        string                                  `json:"schemaVersion"`
	ID                   string                                  `json:"id"`
	Context              string                                  `json:"context"`
	Target               string                                  `json:"target"`
	CreatedAt            string                                  `json:"createdAt"`
	InventoryComplete    bool                                    `json:"inventoryComplete"`
	Source               *unstructured.Unstructured              `json:"source"`
	Desired              *appsv1.Deployment                      `json:"desired"`
	Services             []corev1.Service                        `json:"services"`
	HPAs                 []autoscalingv2.HorizontalPodAutoscaler `json:"hpas"`
	Blockers             []string                                `json:"blockers,omitempty"`
	Warnings             []string                                `json:"warnings,omitempty"`
	CutoverMode          string                                  `json:"cutoverMode"`
	OverlapJustification string                                  `json:"overlapJustification,omitempty"`
	HealthURL            string                                  `json:"healthURL,omitempty"`
	Recovery             []string                                `json:"recovery"`
	Progress             Progress                                `json:"progress"`
}
type Progress struct {
	Constraints    *ConstraintAssessment  `json:"constraints,omitempty"`
	AdmittedSpec   *appsv1.DeploymentSpec `json:"admittedSpec,omitempty"`
	Phase          string                 `json:"phase"`
	Events         []string               `json:"events,omitempty"`
	DestinationUID string                 `json:"destinationUID,omitempty"`
	SourceVersion  string                 `json:"sourceVersion,omitempty"`
	Error          string                 `json:"error,omitempty"`
}

func planDigest(p *MigrationPlan) string {
	b, _ := json.Marshal(struct {
		Source                      *unstructured.Unstructured
		Desired                     *appsv1.Deployment
		Mode, Justification, Health string
	}{p.Source, p.Desired, p.CutoverMode, p.OverlapJustification, p.HealthURL})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (r *Runner) buildPlan(ctx context.Context, dc *unstructured.Unstructured, c Conversion, live bool) (*MigrationPlan, error) {
	p := &MigrationPlan{SchemaVersion: "v1", ID: migrationID(dc), Context: r.Report.Context, Target: r.Target, CreatedAt: r.Now().UTC().Format("2006-01-02T15:04:05Z"), Source: dc.DeepCopy(), Desired: c.Deployment.DeepCopy(), Blockers: append([]string{}, c.Unsupported...), Warnings: append([]string{}, c.Warnings...), CutoverMode: r.Options.CutoverMode, OverlapJustification: r.Options.OverlapJustification, HealthURL: r.Options.HealthURL, Recovery: []string{"Retain this source snapshot and dependency state; never delete the DC.", "Stop destination and verify its pods have terminated before restoring source replicas and triggers.", "Use --mode rollback --plan-file PATH; inspect uncertain cluster state on connectivity loss."}, Progress: Progress{Phase: "planned", SourceVersion: dc.GetResourceVersion()}}
	if p.CutoverMode == "overlap" {
		if p.OverlapJustification == "" {
			p.Blockers = append(p.Blockers, "overlap requires an operator justification")
		}
		pod := p.Desired.Spec.Template.Spec
		if len(pod.Volumes) > 0 || pod.HostNetwork || pod.HostPID || pod.HostIPC {
			p.Blockers = append(p.Blockers, "overlap with volumes or host namespaces requires manual migration")
		}
		for _, ctr := range pod.Containers {
			for _, port := range ctr.Ports {
				if port.HostPort != 0 {
					p.Blockers = append(p.Blockers, "overlap with host ports is unsafe")
				}
			}
		}
	}
	if dc.GetDeletionTimestamp() != nil {
		p.Blockers = append(p.Blockers, "source is deleting; resolve its lifecycle before migration")
	}
	if len(dc.GetOwnerReferences()) > 0 {
		p.Blockers = append(p.Blockers, "source has owner references; coordinate its owning controller manually")
	}
	if !live {
		return p, nil
	}
	p.Warnings = append(p.Warnings, "Staging at zero does not prove startup capacity. Run --mode preflight after staging; cutover repeats constraints before any source retirement.")
	p.Blockers = append(p.Blockers, unresolvedImageTriggers(dc)...)
	if dc.GetUID() == "" || dc.GetResourceVersion() == "" {
		return nil, errors.New("live plan requires source UID and resourceVersion")
	}
	if r.Kube == nil {
		return nil, errors.New("planning needs Kubernetes interfaces")
	}
	ns := dc.GetNamespace()
	for _, permission := range [][3]string{{"", "services", "list"}, {"autoscaling", "horizontalpodautoscalers", "list"}, {"apps", "deployments", "list"}, {"apps", "replicasets", "list"}, {"apps", "statefulsets", "list"}, {"apps", "daemonsets", "list"}, {"batch", "jobs", "list"}, {"", "replicationcontrollers", "list"}, {"apps.openshift.io", "deploymentconfigs", "list"}} {
		if err := r.access(ctx, ns, permission[0], permission[1], permission[2]); err != nil {
			return nil, err
		}
	}
	for _, api := range [][2]string{{"v1", "services"}, {"autoscaling/v2", "horizontalpodautoscalers"}} {
		if err := r.requireAPI(ctx, api[0], api[1]); err != nil {
			return nil, err
		}
	}
	services, err := r.Kube.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, svc := range services.Items {
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		if labels.SelectorFromSet(svc.Spec.Selector).Matches(labels.Set(c.Deployment.Spec.Template.Labels)) {
			p.Services = append(p.Services, svc)
		}
	}
	rawHPAs, e := r.Dynamic.Resource(schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}).Namespace(ns).List(ctx, metav1.ListOptions{})
	if e != nil {
		return nil, e
	}
	for _, h := range rawHPAs.Items {
		kind, _, _ := unstructured.NestedString(h.Object, "spec", "scaleTargetRef", "kind")
		name, _, _ := unstructured.NestedString(h.Object, "spec", "scaleTargetRef", "name")
		if kind == "DeploymentConfig" && name == dc.GetName() {
			spec, _, e := unstructured.NestedMap(h.Object, "spec")
			if e != nil {
				return nil, e
			}
			if e = validateBaseline(spec, "io.k8s.api.autoscaling.v2.HorizontalPodAutoscalerSpec"); e != nil {
				p.Blockers = append(p.Blockers, "HPA "+h.GetName()+": "+e.Error()+"; freeze and retarget manually to retain newer features")
			}
		}
	}
	hpas, err := r.Kube.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, h := range hpas.Items {
		if h.Spec.ScaleTargetRef.Kind == "DeploymentConfig" && h.Spec.ScaleTargetRef.Name == dc.GetName() {
			p.HPAs = append(p.HPAs, h)
			if h.Spec.ScaleTargetRef.APIVersion != "apps.openshift.io/v1" {
				p.Blockers = append(p.Blockers, "HPA source API version is ambiguous; set an explicit apps.openshift.io/v1 target before planning")
			}
			if len(h.OwnerReferences) > 0 || len(h.Finalizers) > 0 {
				p.Blockers = append(p.Blockers, "HPA has owners/finalizers; coordinate controller manually")
			}
		}
		if h.Spec.ScaleTargetRef.Kind == "Deployment" && h.Spec.ScaleTargetRef.Name == dc.GetName() {
			p.Blockers = append(p.Blockers, "HPA "+h.Name+" already targets destination")
		}
	}
	if len(p.HPAs) > 1 {
		p.Blockers = append(p.Blockers, "multiple HPAs target source; manual consolidation required")
	}
	resources := []schema.GroupVersionResource{dcResource, deploymentResource, {Group: "apps", Version: "v1", Resource: "replicasets"}, {Group: "apps", Version: "v1", Resource: "statefulsets"}, {Group: "apps", Version: "v1", Resource: "daemonsets"}, {Group: "batch", Version: "v1", Resource: "jobs"}, {Version: "v1", Resource: "replicationcontrollers"}}
	ownedDeploymentUID := ""
	for _, resource := range resources {
		controllers, e := r.Dynamic.Resource(resource).Namespace(ns).List(ctx, metav1.ListOptions{})
		if e != nil {
			return nil, fmt.Errorf("inventory %s: %w", resource.Resource, e)
		}
		for i := range controllers.Items {
			obj := &controllers.Items[i]
			if resource == dcResource && obj.GetUID() == dc.GetUID() {
				continue
			}
			if resource == deploymentResource && obj.GetName() == dc.GetName() {
				if ownedDestination(obj, dc) {
					ownedDeploymentUID = string(obj.GetUID())
					continue
				}
				p.Blockers = append(p.Blockers, "destination Deployment already exists with foreign ownership")
				continue
			}
			if resource.Resource == "replicasets" && obj.GetLabels()[migrationLabel] == p.ID {
				controlled := false
				for _, owner := range obj.GetOwnerReferences() {
					if owner.Controller != nil && *owner.Controller && owner.Kind == "Deployment" && string(owner.UID) == ownedDeploymentUID && ownedDeploymentUID != "" {
						controlled = true
					}
				}
				if !controlled {
					p.Blockers = append(p.Blockers, "orphan or foreign migration ReplicaSet "+obj.GetName()+"; inspect and stop it before staging")
				}
				continue
			}
			if resource.Resource == "replicationcontrollers" && sourceController(obj, dc) {
				phase := obj.GetAnnotations()["openshift.io/deployment.phase"]
				if phase != "" && phase != "Complete" && phase != "Failed" {
					p.Blockers = append(p.Blockers, "source rollout "+obj.GetName()+" is "+phase+"; let it finish or cancel it before planning")
				}
				continue
			}
			var selector labels.Selector
			if resource == dcResource || resource.Resource == "replicationcontrollers" {
				m, _, e := unstructured.NestedStringMap(obj.Object, "spec", "selector")
				if e != nil {
					return nil, e
				}
				selector = labels.SelectorFromSet(m)
			} else {
				m, found, e := unstructured.NestedMap(obj.Object, "spec", "selector")
				if e != nil {
					return nil, e
				}
				if !found {
					continue
				}
				b, _ := json.Marshal(m)
				var ls metav1.LabelSelector
				if e = json.Unmarshal(b, &ls); e != nil {
					return nil, e
				}
				selector, e = metav1.LabelSelectorAsSelector(&ls)
				if e != nil {
					return nil, e
				}
			}
			if selector.Matches(labels.Set(c.Deployment.Spec.Template.Labels)) {
				p.Blockers = append(p.Blockers, fmt.Sprintf("pod labels overlap %s/%s", resource.Resource, obj.GetName()))
			}
		}
	}
	sort.Slice(p.Services, func(i, j int) bool { return p.Services[i].Name < p.Services[j].Name })
	sort.Slice(p.HPAs, func(i, j int) bool { return p.HPAs[i].Name < p.HPAs[j].Name })
	sort.Strings(p.Blockers)
	p.InventoryComplete = true
	return p, nil
}

func unresolvedImageTriggers(dc *unstructured.Unstructured) []string {
	triggers, _, _ := unstructured.NestedSlice(dc.Object, "spec", "triggers")
	var blockers []string
	for _, item := range triggers {
		trigger, ok := item.(map[string]interface{})
		if !ok || trigger["type"] != "ImageChange" {
			continue
		}
		automatic, _, _ := unstructured.NestedBool(trigger, "imageChangeParams", "automatic")
		image, _, _ := unstructured.NestedString(trigger, "imageChangeParams", "lastTriggeredImage")
		if automatic && strings.TrimSpace(image) == "" {
			name, _, _ := unstructured.NestedString(trigger, "imageChangeParams", "from", "name")
			blockers = append(blockers, "ImageChange trigger "+name+" is unresolved; finish the initial build/import, wait for the DC image controller, then regenerate the live plan")
		}
	}
	return blockers
}
func sourceController(obj, dc *unstructured.Unstructured) bool {
	for _, owner := range obj.GetOwnerReferences() {
		if owner.Controller != nil && *owner.Controller {
			return owner.UID == dc.GetUID() && owner.Kind == "DeploymentConfig"
		}
	}
	return obj.GetAnnotations()["openshift.io/deployment-config.name"] == dc.GetName() || obj.GetLabels()["openshift.io/deployment-config.name"] == dc.GetName()
}
func ownedDestination(obj, dc *unstructured.Unstructured) bool {
	return obj.GetAnnotations()[sourceAnnotation] == string(dc.GetUID()) && obj.GetLabels()[migrationLabel] == migrationID(dc) && dc.GetUID() != ""
}
func savePlan(path string, p *MigrationPlan) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'))
}
func (r *Runner) validateStoredPlan(p *MigrationPlan) error {
	if p == nil || p.Source == nil || p.Desired == nil {
		return errors.New("incomplete plan")
	}
	if !contains([]string{"exclusive", "overlap"}, p.CutoverMode) {
		return errors.New("invalid cutover procedure")
	}
	if r.isReservedNamespace(p.Source.GetNamespace()) {
		return errors.New("reserved namespace cannot execute a migration")
	}
	if p.SchemaVersion != "v1" || p.Source == nil || p.Desired == nil || p.ID != migrationID(p.Source) || !p.InventoryComplete {
		return errors.New("invalid plan or incomplete live inventory; generate --mode plan first")
	}
	if p.Context == "" || p.Target == "" || p.Context != r.Report.Context || p.Target != r.Target {
		return errors.New("plan target does not match selected context and API endpoint")
	}
	c, err := r.Convert(p.Source)
	if err != nil {
		return err
	}
	if len(c.Unsupported) > 0 || len(p.Blockers) > 0 {
		return fmt.Errorf("unsupported semantics or plan blockers: %v %v", c.Unsupported, p.Blockers)
	}
	if c.Deployment.Name != p.Desired.Name || c.Deployment.Namespace != p.Desired.Namespace || c.Deployment.APIVersion != p.Desired.APIVersion || c.Deployment.Kind != p.Desired.Kind || !reflect.DeepEqual(c.Deployment.Spec, p.Desired.Spec) || !reflect.DeepEqual(c.Deployment.Labels, p.Desired.Labels) || !reflect.DeepEqual(c.Deployment.Annotations, p.Desired.Annotations) {
		return errors.New("plan deployment was changed; regenerate plan")
	}
	return nil
}

func (r *Runner) revalidatePlan(ctx context.Context, p *MigrationPlan) error {
	if err := r.validateStoredPlan(p); err != nil {
		return err
	}
	c, err := r.Convert(p.Source)
	if err != nil {
		return err
	}
	source, err := r.Dynamic.Resource(dcResource).Namespace(p.Source.GetNamespace()).Get(ctx, p.Source.GetName(), metav1.GetOptions{})
	if err != nil {
		return err
	}
	if source.GetUID() != p.Source.GetUID() || source.GetResourceVersion() != p.Progress.SourceVersion {
		return errors.New("stale source UID/resourceVersion; regenerate plan or inspect journal before recovery")
	}
	if !reflect.DeepEqual(source.Object["spec"], p.Source.Object["spec"]) {
		return errors.New("source snapshot differs from live spec")
	}
	planRunner := *r
	planRunner.Options.CutoverMode = p.CutoverMode
	planRunner.Options.OverlapJustification = p.OverlapJustification
	planRunner.Options.HealthURL = p.HealthURL
	fresh, e := planRunner.buildPlan(ctx, source, c, true)
	if e != nil {
		return e
	}
	if len(fresh.Blockers) > 0 {
		return fmt.Errorf("fresh inventory blockers: %v", fresh.Blockers)
	}
	if dependencyDigest(fresh) != dependencyDigest(p) {
		return errors.New("dependent resource inventory changed; regenerate plan")
	}
	for _, svc := range p.Services {
		live, e := r.Kube.CoreV1().Services(svc.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
		if e != nil {
			return e
		}
		if live.UID != svc.UID || !reflect.DeepEqual(live.Spec, svc.Spec) {
			return fmt.Errorf("Service %s changed", svc.Name)
		}
	}
	for _, h := range p.HPAs {
		live, e := r.Kube.AutoscalingV2().HorizontalPodAutoscalers(h.Namespace).Get(ctx, h.Name, metav1.GetOptions{})
		if e != nil && !apierrors.IsNotFound(e) {
			return e
		}
		if e == nil && (live.UID != h.UID || !reflect.DeepEqual(live.Spec, h.Spec)) {
			return fmt.Errorf("HPA %s changed", h.Name)
		}
	}
	return nil
}

func dependencyDigest(p *MigrationPlan) string {
	items := []interface{}{}
	for _, s := range p.Services {
		items = append(items, struct {
			Name string
			UID  string
			Spec corev1.ServiceSpec
		}{s.Name, string(s.UID), s.Spec})
	}
	for _, h := range p.HPAs {
		items = append(items, struct {
			Name string
			UID  string
			Spec autoscalingv2.HorizontalPodAutoscalerSpec
		}{h.Name, string(h.UID), h.Spec})
	}
	b, _ := json.Marshal(items)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
