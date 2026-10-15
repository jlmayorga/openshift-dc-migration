package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
)

const migrationLabel = "migration.openshift-dc.io/id"
const sourceAnnotation = "migration.openshift-dc.io/source-uid"
const imageTriggerAnnotation = "image.openshift.io/triggers"

type Conversion struct {
	Deployment  *appsv1.Deployment
	Warnings    []string
	Unsupported []string
}
type dcSpec struct {
	Replicas             *int32                 `json:"replicas"`
	Selector             map[string]string      `json:"selector"`
	Template             corev1.PodTemplateSpec `json:"template"`
	Paused               bool                   `json:"paused"`
	Test                 bool                   `json:"test"`
	MinReadySeconds      int32                  `json:"minReadySeconds"`
	RevisionHistoryLimit *int32                 `json:"revisionHistoryLimit"`
	Strategy             struct {
		Type          string `json:"type"`
		RollingParams *struct {
			MaxSurge       *intstr.IntOrString `json:"maxSurge"`
			MaxUnavailable *intstr.IntOrString `json:"maxUnavailable"`
		} `json:"rollingParams"`
	} `json:"strategy"`
	Triggers []struct {
		Type              string `json:"type"`
		ImageChangeParams *struct {
			Automatic      bool                   `json:"automatic"`
			ContainerNames []string               `json:"containerNames"`
			From           corev1.ObjectReference `json:"from"`
		} `json:"imageChangeParams"`
	} `json:"triggers"`
}
type imageTrigger struct {
	From      imageFrom `json:"from"`
	FieldPath string    `json:"fieldPath"`
	Paused    bool      `json:"paused"`
}
type imageFrom struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

func migrationID(dc *unstructured.Unstructured) string {
	sum := sha256.Sum256([]byte(dc.GetNamespace() + "/" + dc.GetName() + "/" + string(dc.GetUID())))
	return hex.EncodeToString(sum[:12])
}
func (r *Runner) Convert(dc *unstructured.Unstructured) (Conversion, error) {
	c := Conversion{}
	if dc == nil || dc.GetAPIVersion() != "apps.openshift.io/v1" || dc.GetKind() != "DeploymentConfig" {
		return c, errors.New("expected apps.openshift.io/v1 DeploymentConfig")
	}
	if len(validation.IsDNS1123Subdomain(dc.GetName())) > 0 || len(validation.IsDNS1123Label(dc.GetNamespace())) > 0 {
		return c, errors.New("source requires a valid name and explicit namespace")
	}
	raw, found, err := unstructured.NestedMap(dc.Object, "spec")
	if err != nil || !found {
		return c, errors.New("source spec must be an object")
	}
	pod, found, err := unstructured.NestedMap(raw, "template")
	if err != nil || !found {
		return c, errors.New("source spec.template must be an object")
	}
	if err = validateBaseline(pod, "io.k8s.api.core.v1.PodTemplateSpec"); err != nil {
		return c, err
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return c, err
	}
	var s dcSpec
	if err = json.Unmarshal(data, &s); err != nil {
		return c, fmt.Errorf("invalid DC spec: %w", err)
	}
	if len(s.Selector) == 0 {
		return c, errors.New("source selector must be nonempty")
	}
	for k, v := range s.Selector {
		if actual, found := s.Template.Labels[k]; !found || actual != v {
			return c, fmt.Errorf("source selector %s=%s does not match pod labels", k, v)
		}
	}
	for key, value := range s.Template.Labels {
		if len(validation.IsQualifiedName(key)) > 0 || len(validation.IsValidLabelValue(value)) > 0 {
			return c, errors.New("invalid pod label")
		}
	}
	if s.Template.Spec.ActiveDeadlineSeconds != nil || len(s.Template.Spec.EphemeralContainers) > 0 {
		return c, errors.New("Deployment templates cannot contain activeDeadlineSeconds or ephemeralContainers")
	}
	if len(s.Template.Spec.Containers) == 0 {
		return c, errors.New("source requires at least one container")
	}
	names := map[string]bool{}
	for _, ctr := range s.Template.Spec.Containers {
		if len(validation.IsDNS1123Label(ctr.Name)) > 0 || names[ctr.Name] || ctr.Image == "" {
			return c, errors.New("containers require distinct valid names and nonempty images")
		}
		names[ctr.Name] = true
	}
	if s.Replicas == nil {
		v := int32(0)
		s.Replicas = &v
		c.Warnings = append(c.Warnings, "Omitted DC replicas defaults to 0; destination remains scaled to zero.")
	}
	if *s.Replicas < 0 || s.MinReadySeconds < 0 || (s.RevisionHistoryLimit != nil && *s.RevisionHistoryLimit < 0) {
		return c, errors.New("replicas, minReadySeconds and revisionHistoryLimit must be nonnegative")
	}
	if s.Template.Spec.RestartPolicy != "" && s.Template.Spec.RestartPolicy != corev1.RestartPolicyAlways {
		return c, errors.New("Deployment pods require restartPolicy Always")
	}
	deadline := int32(600)
	if s.MinReadySeconds >= deadline {
		if s.MinReadySeconds == math.MaxInt32 {
			return c, errors.New("minReadySeconds leaves no valid Deployment progress deadline; manual migration required")
		}
		deadline = s.MinReadySeconds + 1
		c.Warnings = append(c.Warnings, "Deployment progress deadline raised above preserved minReadySeconds.")
	}
	id := migrationID(dc)
	dep := &appsv1.Deployment{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}, ObjectMeta: metav1.ObjectMeta{Name: dc.GetName(), Namespace: dc.GetNamespace(), Annotations: map[string]string{sourceAnnotation: string(dc.GetUID())}}, Spec: appsv1.DeploymentSpec{Replicas: s.Replicas, Paused: s.Paused, MinReadySeconds: s.MinReadySeconds, ProgressDeadlineSeconds: &deadline, RevisionHistoryLimit: s.RevisionHistoryLimit, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{migrationLabel: id}}, Template: *s.Template.DeepCopy()}}
	if r.Options.PreserveLabels {
		dep.Labels = maps.Clone(dc.GetLabels())
		for _, k := range dcSpecificLabels {
			delete(dep.Labels, k)
		}
	}
	if r.Options.PreserveAnnotations {
		for k, v := range dc.GetAnnotations() {
			if !contains(dcSpecificAnnotations, k) && k != imageTriggerAnnotation && k != "kubectl.kubernetes.io/last-applied-configuration" {
				dep.Annotations[k] = v
			}
		}
	}
	dep.Annotations[sourceAnnotation] = string(dc.GetUID())
	delete(dep.Annotations, "openshift.io/migration-timestamp")
	dep.Labels = ensureMap(dep.Labels)
	dep.Labels[migrationLabel] = id
	dep.Spec.Template.Labels = ensureMap(dep.Spec.Template.Labels)
	if old := dep.Spec.Template.Labels[migrationLabel]; old != "" {
		return c, errors.New("source uses reserved migration label")
	}
	dep.Spec.Template.Labels[migrationLabel] = id

	for _, key := range dcSpecificAnnotations {
		delete(dep.Spec.Template.Annotations, key)
	}
	if _, found := s.Template.Labels["pod-template-hash"]; found {
		c.Unsupported = append(c.Unsupported, "pod-template-hash is replaced by Deployment controllers; review dependent selectors manually")
	}
	if len(s.Template.OwnerReferences) > 0 || len(s.Template.Finalizers) > 0 {
		c.Unsupported = append(c.Unsupported, "pod template owner references/finalizers require a manual lifecycle review")
	}
	dep.Spec.Template.ObjectMeta = metav1.ObjectMeta{Labels: dep.Spec.Template.Labels, Annotations: dep.Spec.Template.Annotations}
	c.Deployment = dep
	for key := range raw {
		if !contains([]string{"replicas", "selector", "template", "paused", "test", "minReadySeconds", "revisionHistoryLimit", "strategy", "triggers"}, key) {
			c.Unsupported = append(c.Unsupported, "spec."+key+" needs a manual equivalent")
		}
	}
	strategy, _, _ := unstructured.NestedMap(raw, "strategy")
	for key, val := range strategy {
		if !contains([]string{"type", "rollingParams", "recreateParams", "resources", "activeDeadlineSeconds", "annotations", "labels"}, key) {
			c.Unsupported = append(c.Unsupported, "strategy."+key+" needs manual migration")
		}
		if contains([]string{"resources", "annotations", "labels"}, key) && nonempty(val) {
			c.Unsupported = append(c.Unsupported, "deployer "+key+" has no Deployment equivalent")
		}
	}
	switch s.Strategy.Type {
	case "", "Rolling":
		dep.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
		dep.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{MaxSurge: intstrPtr("25%"), MaxUnavailable: intstrPtr("25%")}
		if p := s.Strategy.RollingParams; p != nil {
			if p.MaxSurge != nil {
				dep.Spec.Strategy.RollingUpdate.MaxSurge = p.MaxSurge
			}
			if p.MaxUnavailable != nil {
				dep.Spec.Strategy.RollingUpdate.MaxUnavailable = p.MaxUnavailable
			}
		}
		for index, v := range []*intstr.IntOrString{dep.Spec.Strategy.RollingUpdate.MaxSurge, dep.Spec.Strategy.RollingUpdate.MaxUnavailable} {
			if err := validateLimit(v, index == 1); err != nil {
				return c, err
			}
		}
		ru := dep.Spec.Strategy.RollingUpdate
		if intstrValueZero(ru.MaxSurge) && intstrValueZero(ru.MaxUnavailable) {
			return c, errors.New("maxSurge and maxUnavailable cannot both be zero")
		}
	case "Recreate":
		dep.Spec.Strategy.Type = appsv1.RecreateDeploymentStrategyType
	default:
		c.Unsupported = append(c.Unsupported, "strategy "+s.Strategy.Type+": replace custom deployer with an explicit supported strategy")
	}
	if hasLifecycleHooks(dc) {
		c.Unsupported = append(c.Unsupported, "deployment hooks: migrate hooks to reviewed Jobs or application lifecycle handling")
	}
	for _, p := range []string{"rollingParams", "recreateParams"} {
		params, _, _ := unstructured.NestedMap(strategy, p)
		for key := range params {
			if !contains([]string{"maxSurge", "maxUnavailable", "intervalSeconds", "updatePeriodSeconds", "timeoutSeconds", "pre", "mid", "post"}, key) {
				c.Unsupported = append(c.Unsupported, "strategy."+p+"."+key+" has no supported equivalent")
			}
		}
	}
	if s.Test {
		c.Unsupported = append(c.Unsupported, "test DeploymentConfig: use a separately reviewed Job")
	}
	c.Warnings = append(c.Warnings, "Deployment rollout failure does not guarantee DC automatic rollback; use the explicit recovery procedure.", "DC deployer timing and deadline behavior is not reproduced; Deployment readiness uses its own deadline.")
	rawTriggers, _, _ := unstructured.NestedSlice(raw, "triggers")
	for _, value := range rawTriggers {
		tr, ok := value.(map[string]interface{})
		if !ok {
			return c, errors.New("trigger must be an object")
		}
		for key := range tr {
			if !contains([]string{"type", "imageChangeParams"}, key) {
				c.Unsupported = append(c.Unsupported, "trigger field "+key+" is unsupported")
			}
		}
		if p, ok := tr["imageChangeParams"].(map[string]interface{}); ok {
			for key := range p {
				if !contains([]string{"automatic", "containerNames", "from", "lastTriggeredImage"}, key) {
					c.Unsupported = append(c.Unsupported, "image trigger field "+key+" is unsupported")
				}
			}
		}
	}
	configAuto := false
	_, triggersPresent := raw["triggers"]
	if !triggersPresent || raw["triggers"] == nil {
		configAuto = true
		c.Warnings = append(c.Warnings, "Omitted triggers uses the DC API ConfigChange default.")
	}
	var triggers []imageTrigger
	targeted := map[string]bool{}
	for _, tr := range s.Triggers {
		switch tr.Type {
		case "ConfigChange":
			if tr.ImageChangeParams != nil {
				c.Unsupported = append(c.Unsupported, "ConfigChange cannot carry ImageChange parameters")
			}
			configAuto = true
		case "ImageChange":
			p := tr.ImageChangeParams
			if p == nil || p.From.Kind != "ImageStreamTag" || len(p.ContainerNames) == 0 || !validImageStreamTag(p.From.Name) {
				c.Unsupported = append(c.Unsupported, "ImageChange requires ImageStreamTag name:tag and explicit containers")
				continue
			}
			if p.From.UID != "" || p.From.ResourceVersion != "" || p.From.FieldPath != "" || (p.From.APIVersion != "" && p.From.APIVersion != "image.openshift.io/v1") {
				c.Unsupported = append(c.Unsupported, "ImageChange has pinned/unsupported source reference fields")
				continue
			}
			if p.From.Namespace != "" && len(validation.IsDNS1123Label(p.From.Namespace)) > 0 {
				return c, errors.New("invalid ImageStream source namespace")
			}
			for _, name := range p.ContainerNames {
				if !names[name] || targeted[name] {
					c.Unsupported = append(c.Unsupported, "ImageChange container "+name+" is missing or targeted more than once")
					continue
				}
				targeted[name] = true
				triggers = append(triggers, imageTrigger{From: imageFrom{Kind: "ImageStreamTag", Name: p.From.Name, Namespace: p.From.Namespace}, FieldPath: fmt.Sprintf("spec.template.spec.containers[?(@.name==%q)].image", name), Paused: !p.Automatic || s.Paused})
			}
		default:
			c.Unsupported = append(c.Unsupported, "trigger "+tr.Type+" needs manual migration")
		}
	}
	if !configAuto {
		dep.Spec.Paused = true
		c.Unsupported = append(c.Unsupported, "ConfigChange is disabled: Deployment template edits roll out automatically; keep paused and design a manual rollout procedure")
	}
	if len(triggers) > 0 {
		b, _ := json.Marshal(triggers)
		dep.Annotations[imageTriggerAnnotation] = string(b)
	}
	if len(c.Unsupported) > 0 {
		dep.Spec.Paused = true
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(dep)
	if err != nil {
		return c, err
	}
	if err = validateBaseline(obj, "io.k8s.api.apps.v1.Deployment"); err != nil {
		return c, err
	}
	return c, nil
}
func (r *Runner) convertDCtoDeployment(dc *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	c, err := r.Convert(dc)
	if err != nil {
		return nil, err
	}
	if len(c.Unsupported) > 0 {
		return nil, fmt.Errorf("manual migration required: %s", strings.Join(c.Unsupported, "; "))
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(c.Deployment)
	return &unstructured.Unstructured{Object: obj}, err
}
func ensureMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
func nonempty(v interface{}) bool {
	switch x := v.(type) {
	case map[string]interface{}:
		return len(x) > 0
	case nil:
		return false
	default:
		return true
	}
}
func intstrPtr(s string) *intstr.IntOrString { v := intstr.FromString(s); return &v }
func intstrValueZero(v *intstr.IntOrString) bool {
	return v != nil && ((v.Type == intstr.Int && v.IntVal == 0) || (v.Type == intstr.String && v.StrVal == "0%"))
}
func validateLimit(v *intstr.IntOrString, unavailable bool) error {
	if v.Type == intstr.Int {
		if v.IntVal < 0 {
			return errors.New("rolling limits must be nonnegative")
		}
		return nil
	}
	if !strings.HasSuffix(v.StrVal, "%") {
		return errors.New("rolling limit must be an integer or percentage")
	}
	n, err := intstr.GetScaledValueFromIntOrPercent(v, 100, false)
	if err != nil || n < 0 || (unavailable && n > 100) {
		return errors.New("rolling percentage must be nonnegative; maxUnavailable cannot exceed 100%")
	}
	return nil
}

func validImageStreamTag(name string) bool {
	stream, tag, found := strings.Cut(name, ":")
	return found && len(validation.IsDNS1123Subdomain(stream)) == 0 && regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`).MatchString(tag)
}
