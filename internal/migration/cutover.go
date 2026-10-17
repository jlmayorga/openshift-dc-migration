package migration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

func (r *Runner) checkpoint(p *MigrationPlan, phase, event string) error {
	p.Progress.Phase = phase
	p.Progress.Events = append(p.Progress.Events, event)
	p.Progress.Error = ""
	return savePlan(r.Options.PlanFile, p)
}
func (r *Runner) runMigration(ctx context.Context, p *MigrationPlan, result *Result) error {
	if err := r.validateStoredPlan(p); err != nil {
		return err
	}
	ns := p.Source.GetNamespace()
	perms := [][3]string{{"apps.openshift.io", "deploymentconfigs", "get"}, {"apps.openshift.io", "deploymentconfigs", "update"}, {"apps", "deployments", "get"}, {"apps", "deployments", "update"}, {"apps", "replicasets", "list"}, {"", "replicationcontrollers", "list"}, {"", "pods", "list"}, {"", "endpoints", "get"}}
	if len(p.HPAs) > 0 {
		perms = append(perms, [][3]string{{"autoscaling", "horizontalpodautoscalers", "get"}, {"autoscaling", "horizontalpodautoscalers", "delete"}, {"autoscaling", "horizontalpodautoscalers", "create"}}...)
	}
	for _, perm := range perms {
		if err := r.access(ctx, ns, perm[0], perm[1], perm[2]); err != nil {
			return err
		}
	}
	if r.Options.Mode == "rollback" {
		return r.rollback(ctx, p, result)
	}
	if blockers := unresolvedImageTriggers(p.Source); len(blockers) > 0 {
		return fmt.Errorf("cutover blocked: %v", blockers)
	}
	if !r.Options.AutomationQuiesced {
		return errors.New("cutover requires --automation-quiesced after pausing external reconcilers and scaling automation")
	}
	if p.Desired.Spec.Paused {
		return errors.New("paused source requires a reviewed activation procedure; automatic cutover blocked")
	}
	if *p.Desired.Spec.Replicas > 0 {
		if err := validateHealthURL(p.HealthURL); err != nil {
			return err
		}
	}
	if p.Progress.Phase == "complete" {
		if err := r.verifyCutover(ctx, p); err != nil {
			return err
		}
		result.Ready = true
		result.Applied = true
		result.Outcome = "ready"
		return nil
	}
	if p.Progress.Phase != "staged" {
		return errors.New("cutover requires staged plan; interrupted/failed cutover requires explicit rollback before replanning")
	}
	if err := r.revalidatePlan(ctx, p); err != nil {
		return err
	}

	staged, err := r.destination(ctx, p)
	if err != nil {
		return err
	}
	if p.Progress.AdmittedSpec == nil || !reflect.DeepEqual(staged.Spec, *p.Progress.AdmittedSpec) || staged.Spec.Replicas == nil || *staged.Spec.Replicas != 0 || !staged.Spec.Paused || staged.Annotations[imageTriggerAnnotation] != "" {
		return errors.New("staged destination changed; refusing source retirement")
	}
	if err := r.assessConstraints(ctx, p, staged, result); err != nil {
		return err
	}
	if err := r.checkpoint(p, "cutover-started", "begin cutover; failures require explicit rollback"); err != nil {
		return err
	}
	// Freeze autoscalers before changing either controller's replica target.
	for _, h := range p.HPAs {
		if err := r.freezeHPA(ctx, p, h); err != nil {
			return err
		}
	}
	if err := r.changeSource(ctx, p, false, false); err != nil {
		return err
	}
	if p.CutoverMode == "exclusive" {
		if err := r.changeSource(ctx, p, true, false); err != nil {
			return err
		}
		if err := r.waitSourceStopped(ctx, p); err != nil {
			return err
		}
		if err := r.parkSource(ctx, p); err != nil {
			return err
		}
	}
	if err := r.changeDestination(ctx, p, true); err != nil {
		return err
	}
	result.Applied = true
	if err := r.waitReady(ctx, p); err != nil {
		return err
	}
	if err := r.checkTraffic(ctx, p); err != nil {
		return err
	}
	if err := r.checkHealth(ctx, p); err != nil {
		return err
	}
	if p.CutoverMode == "overlap" {
		if err := r.changeSource(ctx, p, true, false); err != nil {
			return err
		}
		if err := r.waitSourceStopped(ctx, p); err != nil {
			return err
		}
		if err := r.parkSource(ctx, p); err != nil {
			return err
		}
	}
	for _, h := range p.HPAs {
		if err := r.createHPA(ctx, p, h, false); err != nil {
			return err
		}
	}
	if err := r.verifyCutover(ctx, p); err != nil {
		return err
	}
	if err := r.checkpoint(p, "complete", "verified source retired, destination ready, Service routing and application health"); err != nil {
		return err
	}
	result.Ready = true
	result.Outcome = "ready"
	return nil
}
func sourceSpec(p *MigrationPlan, stop, restore bool) map[string]interface{} {
	obj := p.Source.DeepCopy()
	if !restore {
		unstructured.RemoveNestedField(obj.Object, "spec", "paused")
		_ = unstructured.SetNestedSlice(obj.Object, []interface{}{}, "spec", "triggers")
		if stop {
			_ = unstructured.SetNestedField(obj.Object, int64(0), "spec", "replicas")
		}
	}
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
	return spec
}
func parkedSourceSpec(p *MigrationPlan) map[string]interface{} {
	spec := sourceSpec(p, true, false)
	spec["paused"] = true
	return spec
}
func (r *Runner) parkSource(ctx context.Context, p *MigrationPlan) error {
	return r.setSourceSpec(ctx, p, parkedSourceSpec(p), "pause retired source after replica reconciliation")
}
func (r *Runner) changeSource(ctx context.Context, p *MigrationPlan, stop, restore bool) error {
	event := "quiesce source triggers while allowing replica reconciliation"
	if stop {
		event = "retire source at zero replicas"
	}
	if restore {
		event = "restore source snapshot"
	}
	return r.setSourceSpec(ctx, p, sourceSpec(p, stop, restore), event)
}
func (r *Runner) setSourceSpec(ctx context.Context, p *MigrationPlan, desired map[string]interface{}, event string) error {
	api := r.Dynamic.Resource(dcResource).Namespace(p.Source.GetNamespace())
	const attempts = 5
	delay := 100 * time.Millisecond
	var expected map[string]interface{}
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := api.Get(ctx, p.Source.GetName(), metav1.GetOptions{})
		if err != nil {
			return err
		}
		if current.GetDeletionTimestamp() != nil {
			return errors.New("source is deleting; refusing mutation")
		}
		if current.GetUID() != p.Source.GetUID() {
			return errors.New("source replaced; refusing mutation")
		}
		spec, _, err := unstructured.NestedMap(current.Object, "spec")
		if err != nil {
			return err
		}
		if attempt == 1 {
			if !reflect.DeepEqual(spec, sourceSpec(p, false, true)) && !reflect.DeepEqual(spec, sourceSpec(p, false, false)) && !reflect.DeepEqual(spec, sourceSpec(p, true, false)) && !reflect.DeepEqual(spec, parkedSourceSpec(p)) {
				return errors.New("source semantics changed outside migration; manual recovery required")
			}
			expected = spec
		} else if !reflect.DeepEqual(spec, expected) {
			return errors.New("source spec changed during conflict retry; explicit recovery required")
		}
		if reflect.DeepEqual(spec, desired) {
			p.Progress.SourceVersion = current.GetResourceVersion()
			return nil
		}
		if err = r.checkpoint(p, "source-intent", "intent: "+event); err != nil {
			return err
		}
		_ = unstructured.SetNestedMap(current.Object, desired, "spec")
		admitted, err := api.Update(ctx, current, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
		stage := "source update dry run"
		if err == nil {
			if admitted.GetUID() != p.Source.GetUID() || !reflect.DeepEqual(admitted.Object["spec"], desired) {
				return errors.New("source admission changed planned semantics")
			}
			var updated *unstructured.Unstructured
			updated, err = api.Update(ctx, current, metav1.UpdateOptions{FieldValidation: "Strict"})
			stage = event
			if err == nil {
				if updated.GetUID() != p.Source.GetUID() || !reflect.DeepEqual(updated.Object["spec"], desired) {
					return errors.New("source persisted admission differs; state uncertain")
				}
				p.Progress.SourceVersion = updated.GetResourceVersion()
				return r.checkpoint(p, "source-updated", "confirmed: "+event)
			}
			if !apierrors.IsConflict(err) {
				return fmt.Errorf("%s (state may be uncertain): %w", event, err)
			}
		} else if !apierrors.IsConflict(err) {
			return fmt.Errorf("source update dry run: %w", err)
		}
		// A definite conflict rejected this update. Refetch and repeat admission,
		// accepting status/metadata churn only; never replay an uncertain write.
		if attempt == attempts {
			return fmt.Errorf("%s conflict after %d attempts; explicit recovery required: %w", stage, attempts, err)
		}
		if err = r.checkpoint(p, "source-intent", fmt.Sprintf("%s conflict; retry %d of %d will refetch and revalidate source identity/spec", stage, attempt+1, attempts)); err != nil {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay *= 2
	}
}
func (r *Runner) destination(ctx context.Context, p *MigrationPlan) (*appsv1.Deployment, error) {
	_, d, err := r.destinationState(ctx, p)
	return d, err
}
func (r *Runner) destinationState(ctx context.Context, p *MigrationPlan) (*unstructured.Unstructured, *appsv1.Deployment, error) {
	object, d, err := r.getDeploymentState(ctx, p.Desired.Namespace, p.Desired.Name, r.Options.Mode != "rollback")
	if err != nil {
		return nil, nil, err
	}
	if d.DeletionTimestamp != nil {
		return nil, nil, errors.New("destination is deleting; manual inspection required")
	}
	if d.Annotations[sourceAnnotation] != string(p.Source.GetUID()) || d.Annotations[planAnnotation] != planDigest(p) || d.Labels[migrationLabel] != p.ID {
		return nil, nil, errors.New("destination ownership/UID changed; refusing mutation")
	}
	if p.Progress.DestinationUID == "" {
		if p.Progress.AdmittedSpec == nil || !reflect.DeepEqual(d.Spec, *p.Progress.AdmittedSpec) || d.Spec.Replicas == nil || *d.Spec.Replicas != 0 || !d.Spec.Paused {
			return nil, nil, errors.New("cannot recover unknown destination UID from non-staged state")
		}
		p.Progress.DestinationUID = string(d.UID)
		if err = savePlan(r.Options.PlanFile, p); err != nil {
			return nil, nil, err
		}
	}
	if string(d.UID) != p.Progress.DestinationUID {
		return nil, nil, errors.New("destination UID changed")
	}
	return object, d, nil
}
func (r *Runner) changeDestination(ctx context.Context, p *MigrationPlan, activate bool) error {
	original, d, err := r.destinationState(ctx, p)
	if err != nil {
		return err
	}
	if p.Progress.AdmittedSpec == nil {
		return errors.New("missing admitted staging spec")
	}
	expected := p.Progress.AdmittedSpec.DeepCopy()
	expected.Replicas = d.Spec.Replicas
	expected.Paused = d.Spec.Paused
	if activate && !reflect.DeepEqual(d.Spec, *expected) {
		return errors.New("destination spec changed outside migration; manual review required")
	}
	if activate && p.CutoverMode == "exclusive" {
		source, e := r.Dynamic.Resource(dcResource).Namespace(p.Source.GetNamespace()).Get(ctx, p.Source.GetName(), metav1.GetOptions{})
		if e != nil {
			return e
		}
		if source.GetUID() != p.Source.GetUID() || !reflect.DeepEqual(source.Object["spec"], parkedSourceSpec(p)) {
			return errors.New("source not retired; refusing activation")
		}
	}
	d.Spec.Paused = !activate
	zero := int32(0)
	d.Spec.Replicas = &zero
	delete(d.Annotations, imageTriggerAnnotation)
	if activate {
		replicas := *p.Desired.Spec.Replicas
		d.Spec.Replicas = &replicas
		if tr := p.Desired.Annotations[imageTriggerAnnotation]; tr != "" {
			d.Annotations[imageTriggerAnnotation] = tr
		}
	}
	if err = r.checkpoint(p, "destination-intent", fmt.Sprintf("intent: set destination replicas=%d paused=%t", *d.Spec.Replicas, d.Spec.Paused)); err != nil {
		return err
	}
	// Preserve the server's representation, including omitted null metadata.
	// Recovery also retains fields outside this client's typed model.
	request := original.DeepCopy()
	_ = unstructured.SetNestedField(request.Object, int64(*d.Spec.Replicas), "spec", "replicas")
	if d.Spec.Paused {
		_ = unstructured.SetNestedField(request.Object, true, "spec", "paused")
	} else {
		unstructured.RemoveNestedField(request.Object, "spec", "paused")
	}
	request.SetAnnotations(d.Annotations)
	admitted, err := r.updateResource(ctx, deploymentResource, "Deployment", request, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
	if err != nil {
		return fmt.Errorf("destination update dry run: %w", err)
	}
	if activate {
		if err = validateResourceSpec(admitted, deploymentSpecSchema); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(admitted.Object["spec"], request.Object["spec"]) || admitted.GetAnnotations()[imageTriggerAnnotation] != request.GetAnnotations()[imageTriggerAnnotation] {
		return errors.New("admission changed activation/recovery spec; manual review required")
	}
	updated, err := r.updateResource(ctx, deploymentResource, "Deployment", request, metav1.UpdateOptions{FieldValidation: "Strict"})
	if err != nil {
		return fmt.Errorf("destination update uncertain: %w", err)
	}
	if activate {
		if err = validateResourceSpec(updated, deploymentSpecSchema); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(updated.Object["spec"], request.Object["spec"]) || updated.GetAnnotations()[imageTriggerAnnotation] != request.GetAnnotations()[imageTriggerAnnotation] {
		return errors.New("persisted destination differs from dry run; inspect immediately")
	}
	return r.checkpoint(p, "destination-updated", "confirmed destination update")
}
func (r *Runner) freezeHPA(ctx context.Context, p *MigrationPlan, h autoscalingv2.HorizontalPodAutoscaler) error {
	api := r.Kube.AutoscalingV2().HorizontalPodAutoscalers(h.Namespace)
	current, err := r.getHPA(ctx, h.Namespace, h.Name)
	if err != nil {
		return err
	}
	if current.UID != h.UID || !reflect.DeepEqual(current.Spec, h.Spec) {
		return errors.New("HPA changed before freeze")
	}
	if err = r.checkpoint(p, "hpa-freeze-intent", "intent: temporarily remove HPA "+h.Name+" (snapshot retained)"); err != nil {
		return err
	}
	uid, rv := current.UID, current.ResourceVersion
	if err = api.Delete(ctx, h.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}); err != nil {
		return err
	}
	return r.poll(ctx, func(ctx context.Context) (bool, error) {
		_, err := api.Get(ctx, h.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
}
func cleanHPA(h autoscalingv2.HorizontalPodAutoscaler) *autoscalingv2.HorizontalPodAutoscaler {
	d := h.DeepCopy()
	d.TypeMeta = metav1.TypeMeta{APIVersion: "autoscaling/v2", Kind: "HorizontalPodAutoscaler"}
	d.UID = ""
	d.ResourceVersion = ""
	d.Generation = 0
	d.CreationTimestamp = metav1.Time{}
	d.ManagedFields = nil
	d.Status = autoscalingv2.HorizontalPodAutoscalerStatus{}
	return d
}
func (r *Runner) createHPA(ctx context.Context, p *MigrationPlan, h autoscalingv2.HorizontalPodAutoscaler, restore bool) error {
	desired := cleanHPA(h)
	if !restore {
		desired.Spec.ScaleTargetRef = autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: p.Desired.Name}
	}
	desired.Labels = ensureMap(desired.Labels)
	desired.Labels[migrationLabel] = p.ID
	desired.Annotations = ensureMap(desired.Annotations)
	desired.Annotations[planAnnotation] = planDigest(p)
	current, err := r.getHPA(ctx, h.Namespace, h.Name)
	if err == nil {
		if current.DeletionTimestamp != nil {
			return errors.New("HPA is deleting; cannot claim a restored/retargeted autoscaler")
		}
		if reflect.DeepEqual(current.Spec, desired.Spec) && ((restore && current.UID == h.UID) || (current.Labels[migrationLabel] == p.ID && current.Annotations[planAnnotation] == planDigest(p))) {
			return nil
		}
		return errors.New("HPA destination name is occupied; refusing overwrite")
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	if err = r.checkpoint(p, "hpa-create-intent", "intent: recreate HPA "+h.Name+" with reviewed target"); err != nil {
		return err
	}
	admitted, err := r.createHPAObject(ctx, desired, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(admitted.Spec, desired.Spec) {
		return errors.New("HPA admission changed restored/retargeted semantics")
	}
	created, err := r.createHPAObject(ctx, desired, metav1.CreateOptions{FieldValidation: "Strict"})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(created.Spec, desired.Spec) {
		return errors.New("persisted HPA changed restored/retargeted semantics")
	}
	return nil
}
func (r *Runner) poll(ctx context.Context, check func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, r.Options.ReadinessTimeout)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := check(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		timer := time.NewTimer(r.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (r *Runner) sourceStopped(ctx context.Context, p *MigrationPlan) (bool, error) {
	source, err := r.Dynamic.Resource(dcResource).Namespace(p.Source.GetNamespace()).Get(ctx, p.Source.GetName(), metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if source.GetDeletionTimestamp() != nil {
		return false, errors.New("source is deleting; retirement/recovery cannot be assumed")
	}
	if source.GetUID() != p.Source.GetUID() || (!reflect.DeepEqual(source.Object["spec"], sourceSpec(p, true, false)) && !reflect.DeepEqual(source.Object["spec"], parkedSourceSpec(p))) {
		return false, errors.New("source retirement changed unexpectedly")
	}
	rcs, err := r.Kube.CoreV1().ReplicationControllers(p.Source.GetNamespace()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	for _, rc := range rcs.Items {
		u := &unstructured.Unstructured{}
		u.SetOwnerReferences(rc.OwnerReferences)
		u.SetAnnotations(rc.Annotations)
		u.SetLabels(rc.Labels)
		if sourceController(u, p.Source) && rc.Spec.Replicas != nil && *rc.Spec.Replicas != 0 {
			return false, nil
		}
	}
	selector, _, _ := unstructured.NestedStringMap(p.Source.Object, "spec", "selector")
	pods, err := r.Kube.CoreV1().Pods(p.Source.GetNamespace()).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(selector).String()})
	if err != nil {
		return false, err
	}
	for _, pod := range pods.Items {
		if pod.Labels[migrationLabel] != p.ID {
			return false, nil
		}
	}
	return true, nil
}
func (r *Runner) waitSourceStopped(ctx context.Context, p *MigrationPlan) error {
	return r.poll(ctx, func(ctx context.Context) (bool, error) { return r.sourceStopped(ctx, p) })
}

func (r *Runner) waitReady(ctx context.Context, p *MigrationPlan) error {
	return r.poll(ctx, func(ctx context.Context) (bool, error) {
		if p.CutoverMode == "exclusive" {
			stopped, e := r.sourceStopped(ctx, p)
			if e != nil || !stopped {
				stopErr := r.changeDestination(ctx, p, false)
				return false, errors.Join(errors.New("exclusive source state changed; destination stop requested"), e, stopErr)
			}
		}
		d, err := r.destination(ctx, p)
		if err != nil {
			return false, err
		}
		for _, c := range d.Status.Conditions {
			if c.Type == appsv1.DeploymentProgressing && c.Reason == "ProgressDeadlineExceeded" {
				return false, errors.New("destination rollout exceeded progress deadline")
			}
		}
		n := *d.Spec.Replicas
		return !d.Spec.Paused && d.Status.ObservedGeneration >= d.Generation && d.Status.UpdatedReplicas == n && d.Status.Replicas == n && d.Status.ReadyReplicas >= n && d.Status.AvailableReplicas >= n, nil
	})
}
func (r *Runner) checkTraffic(ctx context.Context, p *MigrationPlan) error {
	if *p.Desired.Spec.Replicas == 0 {
		return nil
	}
	return r.poll(ctx, func(ctx context.Context) (bool, error) {
		pods, err := r.Kube.CoreV1().Pods(p.Desired.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.Set{migrationLabel: p.ID}.String()})
		if err != nil {
			return false, err
		}
		uids := map[types.UID]bool{}
		for _, pod := range pods.Items {
			uids[pod.UID] = true
		}
		for _, svc := range p.Services {
			live, e := r.Kube.CoreV1().Services(svc.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
			if e != nil {
				return false, e
			}
			if live.UID != svc.UID || !reflect.DeepEqual(live.Spec, svc.Spec) {
				return false, errors.New("Service changed during cutover")
			}
			ep, e := r.Kube.CoreV1().Endpoints(svc.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
			if e != nil {
				return false, e
			}
			routed := false
			for _, subset := range ep.Subsets {
				for _, address := range subset.Addresses {
					if address.TargetRef != nil && uids[address.TargetRef.UID] {
						routed = true
					}
				}
			}
			if !routed {
				return false, nil
			}
		}
		return true, nil
	})
}
func validateHealthURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("nonzero cutover requires a credential-free http(s) --health-url in its reviewed plan")
	}
	return nil
}
func (r *Runner) checkHealth(ctx context.Context, p *MigrationPlan) error {
	if *p.Desired.Spec.Replicas == 0 {
		return nil
	}
	if err := validateHealthURL(p.HealthURL); err != nil {
		return err
	}
	var lastError error
	err := r.poll(ctx, func(ctx context.Context) (bool, error) {
		attemptError := r.healthAttempt(ctx, p.HealthURL)
		if attemptError == nil {
			return true, nil
		}
		if ctx.Err() == nil || lastError == nil {
			lastError = attemptError
		}
		return false, nil
	})
	if err != nil {
		return errors.Join(err, lastError)
	}
	return nil
}

func (r *Runner) healthAttempt(ctx context.Context, healthURL string) error {
	if r.HealthCheck != nil {
		return r.HealthCheck(ctx, healthURL)
	}
	client := &http.Client{Timeout: r.Options.RequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("health redirects require an explicit final URL")
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("application health request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("application health returned HTTP %d", response.StatusCode)
	}
	return nil
}
func (r *Runner) verifyCutover(ctx context.Context, p *MigrationPlan) error {
	for _, h := range p.HPAs {
		live, err := r.getHPA(ctx, h.Namespace, h.Name)
		if err != nil {
			return err
		}
		expected := h.Spec.DeepCopy()
		expected.ScaleTargetRef = autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: p.Desired.Name}
		if !reflect.DeepEqual(live.Spec, *expected) || live.Annotations[planAnnotation] != planDigest(p) {
			return errors.New("retargeted HPA state changed")
		}
	}

	if err := r.waitSourceStopped(ctx, p); err != nil {
		return err
	}
	if err := r.waitReady(ctx, p); err != nil {
		return err
	}
	if err := r.checkTraffic(ctx, p); err != nil {
		return err
	}
	return r.checkHealth(ctx, p)
}
func (r *Runner) rollback(ctx context.Context, p *MigrationPlan, result *Result) error {
	if err := r.checkpoint(p, "rollback-started", "begin explicit recovery; destination must stop before source restore"); err != nil {
		return err
	}
	for _, h := range p.HPAs {
		api := r.Kube.AutoscalingV2().HorizontalPodAutoscalers(h.Namespace)
		current, err := r.Dynamic.Resource(hpaResource).Namespace(h.Namespace).Get(ctx, h.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if current.GetUID() == h.UID {
			continue
		}
		if current.GetLabels()[migrationLabel] != p.ID || current.GetAnnotations()[planAnnotation] != planDigest(p) {
			return errors.New("foreign HPA blocks recovery")
		}
		uid, rv := current.GetUID(), current.GetResourceVersion()
		if err = r.checkpoint(p, "hpa-recovery-intent", "intent: remove migrated HPA before stopping destination"); err != nil {
			return err
		}
		if err = api.Delete(ctx, h.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}); err != nil {
			return err
		}

		if err = r.poll(ctx, func(ctx context.Context) (bool, error) {
			_, e := api.Get(ctx, h.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(e) {
				return true, nil
			}
			return false, e
		}); err != nil {
			return err
		}
	}
	_, err := r.destination(ctx, p)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err == nil {
		if err = r.changeDestination(ctx, p, false); err != nil {
			return err
		}
	}
	if err = r.waitDestinationStopped(ctx, p); err != nil {
		return err
	}
	if err = r.changeSource(ctx, p, false, true); err != nil {
		return err
	}
	for _, h := range p.HPAs {
		if err = r.createHPA(ctx, p, h, true); err != nil {
			return err
		}
	}
	source, err := r.Dynamic.Resource(dcResource).Namespace(p.Source.GetNamespace()).Get(ctx, p.Source.GetName(), metav1.GetOptions{})
	if err != nil {
		return err
	}
	if source.GetUID() != p.Source.GetUID() || !reflect.DeepEqual(source.Object["spec"], sourceSpec(p, false, true)) {
		return errors.New("source restoration unverified")
	}
	if *p.Desired.Spec.Replicas > 0 {
		if err = validateHealthURL(p.HealthURL); err != nil {
			return fmt.Errorf("source configuration restored; application health remains unverified: %w", err)
		}
		if err = r.waitSourceReady(ctx, p); err != nil {
			return err
		}
		if err = r.checkHealth(ctx, p); err != nil {
			return err
		}
	}
	if err = r.checkpoint(p, "rolled-back", "verified destination stopped and original source/dependent configuration and health restored"); err != nil {
		return err
	}
	result.Outcome = "restored"
	return nil
}

func (r *Runner) waitSourceReady(ctx context.Context, p *MigrationPlan) error {
	return r.poll(ctx, func(ctx context.Context) (bool, error) {
		source, err := r.Dynamic.Resource(dcResource).Namespace(p.Source.GetNamespace()).Get(ctx, p.Source.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if source.GetUID() != p.Source.GetUID() || !reflect.DeepEqual(source.Object["spec"], sourceSpec(p, false, true)) {
			return false, errors.New("restored source changed during readiness verification")
		}
		available, _, err := unstructured.NestedInt64(source.Object, "status", "availableReplicas")
		if err != nil {
			return false, err
		}
		if available < int64(*p.Desired.Spec.Replicas) {
			return false, nil
		}
		selector, _, _ := unstructured.NestedStringMap(p.Source.Object, "spec", "selector")
		pods, err := r.Kube.CoreV1().Pods(p.Source.GetNamespace()).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(selector).String()})
		if err != nil {
			return false, err
		}
		ready := int32(0)
		uids := map[types.UID]bool{}
		for _, pod := range pods.Items {
			if pod.Labels[migrationLabel] == p.ID {
				continue
			}
			for _, condition := range pod.Status.Conditions {
				if condition.Type == "Ready" && condition.Status == "True" {
					ready++
					uids[pod.UID] = true
				}
			}
		}
		if ready < *p.Desired.Spec.Replicas {
			return false, nil
		}
		for _, svc := range p.Services {
			ep, e := r.Kube.CoreV1().Endpoints(svc.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
			if e != nil {
				return false, e
			}
			found := false
			for _, subset := range ep.Subsets {
				for _, address := range subset.Addresses {
					if address.TargetRef != nil && uids[address.TargetRef.UID] {
						found = true
					}
				}
			}
			if !found {
				return false, nil
			}
		}
		return true, nil
	})
}

func (r *Runner) waitDestinationStopped(ctx context.Context, p *MigrationPlan) error {
	return r.poll(ctx, func(ctx context.Context) (bool, error) {
		d, err := r.destination(ctx, p)
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		if err == nil && (d.Spec.Replicas == nil || *d.Spec.Replicas != 0 || d.Status.Replicas != 0 || d.Status.ObservedGeneration < d.Generation) {
			return false, nil
		}
		pods, e := r.Kube.CoreV1().Pods(p.Desired.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.Set{migrationLabel: p.ID}.String()})
		if e != nil {
			return false, e
		}
		if len(pods.Items) > 0 {
			return false, nil
		}
		sets, e := r.Kube.AppsV1().ReplicaSets(p.Desired.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.Set{migrationLabel: p.ID}.String()})
		if e != nil {
			return false, e
		}
		for _, rs := range sets.Items {
			if (rs.Spec.Replicas != nil && *rs.Spec.Replicas != 0) || rs.Status.Replicas != 0 {
				return false, nil
			}
		}
		return true, nil
	})
}
