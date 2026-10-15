package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const planAnnotation = "migration.openshift-dc.io/plan-digest"

func endpointIdentity(host string) string {
	h := sha256.Sum256([]byte(host))
	return hex.EncodeToString(h[:])
}
func (r *Runner) executePlan(ctx context.Context) (runErr error) {
	p, err := loadPlan(r.Options.PlanFile)
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(r.Options.PlanFile+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("plan lock (inspect stale locks only after checking active processes): %w", err)
	}
	if err = lock.Close(); err != nil {
		return err
	}
	defer os.Remove(r.Options.PlanFile + ".lock")
	result := Result{Source: Identity{Name: p.Source.GetName(), Namespace: p.Source.GetNamespace(), UID: string(p.Source.GetUID())}, Destination: Identity{Name: p.Desired.Name, Namespace: p.Desired.Namespace}, Stage: r.Options.Mode, Outcome: "failed"}
	defer func() {
		if runErr != nil {
			result.Error = runErr.Error()
			p.Progress.Error = runErr.Error()
			runErr = errors.Join(runErr, savePlan(r.Options.PlanFile, p))
		}
		r.Report.Results = append(r.Report.Results, result)
	}()
	if r.Dynamic == nil {
		if err = r.connect(); err != nil {
			return err
		}
	}
	if err = r.preflight(ctx); err != nil {
		return err
	}
	if r.Options.Mode == "cutover" || r.Options.Mode == "rollback" {
		return r.runMigration(ctx, p, &result)
	}
	if err = r.revalidatePlan(ctx, p); err != nil {
		return err
	}
	ns := p.Source.GetNamespace()
	for _, perm := range [][3]string{{"apps.openshift.io", "deploymentconfigs", "get"}, {"apps", "deployments", "get"}, {"apps", "deployments", "create"}} {
		if err = r.access(ctx, ns, perm[0], perm[1], perm[2]); err != nil {
			return err
		}
	}
	if p.Desired.Annotations[imageTriggerAnnotation] != "" {
		if err = r.requireAPI(ctx, "image.openshift.io/v1", "imagestreamtags"); err != nil {
			return err
		}
	}
	desired := stagedDeployment(p)
	existing, err := r.Kube.AppsV1().Deployments(ns).Get(ctx, desired.Name, metav1.GetOptions{})
	if err == nil {
		if existing.DeletionTimestamp != nil {
			return errors.New("destination is deleting; cannot stage it")
		}
		if existing.Annotations[sourceAnnotation] != string(p.Source.GetUID()) || existing.Annotations[planAnnotation] != planDigest(p) || existing.Labels[migrationLabel] != p.ID {
			return errors.New("destination already exists; foreign or different migration ownership")
		}
		if existing.Spec.Replicas == nil || *existing.Spec.Replicas != 0 || !existing.Spec.Paused {
			return errors.New("existing destination is active; refusing to restage")
		}
		if p.Progress.AdmittedSpec == nil || !reflect.DeepEqual(existing.Spec, *p.Progress.AdmittedSpec) || !reflect.DeepEqual(existing.Spec.Selector, desired.Spec.Selector) {
			return errors.New("owned destination selector changed")
		}
		p.Progress.DestinationUID = string(existing.UID)
		p.Progress.Phase = "staged"
		if err = savePlan(r.Options.PlanFile, p); err != nil {
			return err
		}
		if r.Options.Mode == "stage" {
			result.Applied = true
			result.Outcome = "staged"
		} else {
			result.Outcome = "validated"
		}
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	admitted, err := r.Kube.AppsV1().Deployments(ns).Create(ctx, desired, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
	if err != nil {
		return fmt.Errorf("server dry run: %w", err)
	}
	if err = validateAdmission(desired, admitted); err != nil {
		return err
	}
	if !reflect.DeepEqual(desired.Spec, admitted.Spec) {
		p.Warnings = append(p.Warnings, "Server admission/defaulting changed destination spec; inspect staged object before cutover.")
	}
	if r.Options.Mode == "dry-run" {
		result.Outcome = "validated"
		return nil
	}
	p.Progress.AdmittedSpec = admitted.Spec.DeepCopy()
	p.Progress.Phase = "stage-intent"
	p.Progress.Events = append(p.Progress.Events, "intent: create paused zero-replica Deployment")
	if err = savePlan(r.Options.PlanFile, p); err != nil {
		return err
	}
	// Use original request again so admission does not depend on replaying server-generated fields.
	created, err := r.Kube.AppsV1().Deployments(ns).Create(ctx, desired, metav1.CreateOptions{FieldValidation: "Strict"})
	if err != nil {
		return fmt.Errorf("create destination (inspect uncertain state and rerun safely): %w", err)
	}
	result.Applied = true
	p.Progress.DestinationUID = string(created.UID)
	p.Progress.Phase = "staged"
	if err = validateAdmission(desired, created); err != nil {
		return fmt.Errorf("persisted admission differs; inspect zero-replica object: %w", err)
	}
	if err = savePlan(r.Options.PlanFile, p); err != nil {
		return err
	}
	result.Applied = true
	result.Outcome = "staged"
	return nil
}
func loadPlan(path string) (*MigrationPlan, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- explicitly selected local migration plan.
	if err != nil {
		return nil, err
	}
	var p MigrationPlan
	decErr := json.Unmarshal(b, &p)
	if decErr != nil {
		return nil, decErr
	}
	if p.SchemaVersion != "v1" || p.Source == nil || p.Desired == nil {
		return nil, errors.New("invalid v1 migration plan")
	}
	return &p, nil
}
func stagedDeployment(p *MigrationPlan) *appsv1.Deployment {
	d := p.Desired.DeepCopy()
	zero := int32(0)
	d.Spec.Replicas = &zero
	d.Spec.Paused = true
	d.Annotations[planAnnotation] = planDigest(p)
	delete(d.Annotations, imageTriggerAnnotation)
	return d
}
func validateAdmission(want, got *appsv1.Deployment) error {
	if got == nil || got.Spec.Replicas == nil || *got.Spec.Replicas != 0 || !got.Spec.Paused || got.Name != want.Name || got.Namespace != want.Namespace || !reflect.DeepEqual(got.Spec.Selector, want.Spec.Selector) || got.Labels[migrationLabel] != want.Labels[migrationLabel] || got.Annotations[sourceAnnotation] != want.Annotations[sourceAnnotation] || got.Annotations[planAnnotation] != want.Annotations[planAnnotation] || got.Annotations[imageTriggerAnnotation] != "" {
		return errors.New("admission changed protected staging fields")
	}
	for k, v := range want.Spec.Template.Labels {
		if got.Spec.Template.Labels[k] != v {
			return fmt.Errorf("admission changed pod label %s", k)
		}
	}
	if len(want.Spec.Template.Spec.Containers) != len(got.Spec.Template.Spec.Containers) {
		return errors.New("admission changed container topology; review manually")
	}
	for i, ctr := range want.Spec.Template.Spec.Containers {
		if got.Spec.Template.Spec.Containers[i].Name != ctr.Name || got.Spec.Template.Spec.Containers[i].Image != ctr.Image {
			return errors.New("admission changed container identity/image; review manually")
		}
	}
	return nil
}
