package migration

import (
	"context"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func legacyInlineFixtures() map[string]corev1.Volume {
	return map[string]corev1.Volume{
		"cephfs":  {Name: "data", VolumeSource: corev1.VolumeSource{CephFS: &corev1.CephFSVolumeSource{Monitors: []string{"10.0.0.1:6789"}, Path: "/data"}}},
		"rbd":     {Name: "data", VolumeSource: corev1.VolumeSource{RBD: &corev1.RBDVolumeSource{CephMonitors: []string{"10.0.0.1:6789"}, RBDImage: "data"}}},
		"gitRepo": {Name: "checkout", VolumeSource: corev1.VolumeSource{GitRepo: &corev1.GitRepoVolumeSource{Repository: "https://example.test/repo.git", Revision: "main"}}},
	}
}

func TestLegacyInlineVolumesRequireManualMigration(t *testing.T) {
	for plugin, volume := range legacyInlineFixtures() {
		t.Run(plugin, func(t *testing.T) {
			source := fixture(t)
			object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&volume)
			if err != nil {
				t.Fatal(err)
			}
			if err = unstructured.SetNestedSlice(source.Object, []interface{}{object}, "spec", "template", "spec", "volumes"); err != nil {
				t.Fatal(err)
			}
			before := source.DeepCopy()
			c, err := NewRunner(DefaultOptions()).Convert(source)
			if err != nil {
				t.Fatal("read-only review output should remain available", err)
			}
			if len(c.Unsupported) == 0 || !strings.Contains(strings.Join(c.Unsupported, " "), plugin) || !c.Deployment.Spec.Paused {
				t.Fatalf("unsafe volume was not classified for manual migration: %+v", c)
			}
			if !reflect.DeepEqual(c.Deployment.Spec.Template.Spec.Volumes, []corev1.Volume{volume}) || !reflect.DeepEqual(source, before) {
				t.Fatal("volume fields lost or source mutated")
			}
		})
	}
}

func TestPreviouslySavedLegacyVolumePlansAreBlocked(t *testing.T) {
	for plugin, volume := range legacyInlineFixtures() {
		for _, mode := range []string{"stage", "preflight", "cutover"} {
			t.Run(plugin+"/"+mode, func(t *testing.T) {
				r, c, path := cutoverTest(t, "exclusive", true)
				p, err := loadPlan(path)
				if err != nil {
					t.Fatal(err)
				}
				object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&volume)
				if err != nil {
					t.Fatal(err)
				}
				// Reconstruct a plan and matching paused destination produced before
				// this guard existed; no current conversion step can bless it.
				if err = unstructured.SetNestedSlice(p.Source.Object, []interface{}{object}, "spec", "template", "spec", "volumes"); err != nil {
					t.Fatal(err)
				}
				c.source = p.Source.DeepCopy()
				p.Desired.Spec.Template.Spec.Volumes = []corev1.Volume{volume}
				c.destination.Spec.Template.Spec.Volumes = []corev1.Volume{volume}
				c.destination.Annotations[planAnnotation] = planDigest(p)
				p.Progress.AdmittedSpec = c.destination.Spec.DeepCopy()
				if err = savePlan(path, p); err != nil {
					t.Fatal(err)
				}
				r.Options.Mode = mode
				before := c.source.DeepCopy()
				hpa := c.hpas[0].DeepCopy()
				destination := c.destination.DeepCopy()
				err = r.Run(context.Background())
				if err == nil || !strings.Contains(err.Error(), plugin) {
					t.Fatalf("saved unsafe plan accepted or wrong refusal: %v", err)
				}
				if len(c.mutations) != 0 || !reflect.DeepEqual(c.source, before) || !reflect.DeepEqual(c.hpas[0], *hpa) || !reflect.DeepEqual(c.destination, destination) {
					t.Fatal("saved plan changed workload/HPA before refusal", c.mutations)
				}
			})
		}
	}
}

func TestAdmissionAddedLegacyStorageBlocksBeforeRetirement(t *testing.T) {
	for plugin, volume := range legacyInlineFixtures() {
		t.Run(plugin, func(t *testing.T) {
			r, c, path := cutoverTest(t, "exclusive", true)
			c.injectedPodVolumes = []corev1.Volume{volume}
			assertStorageRefusal(t, r, c, path, plugin)
		})
	}
	for _, plugin := range []string{"cephfs", "rbd"} {
		t.Run("PVC/"+plugin, func(t *testing.T) {
			r, c, path := cutoverTest(t, "exclusive", true)
			c.injectedPodVolumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}}
			c.claims = map[string]corev1.PersistentVolumeClaim{"/api/v1/namespaces/example/persistentvolumeclaims/data": {ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "example"}, Spec: corev1.PersistentVolumeClaimSpec{VolumeName: "legacy-pv"}, Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound}}}
			pv := corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "legacy-pv"}}
			if plugin == "cephfs" {
				pv.Spec.CephFS = &corev1.CephFSPersistentVolumeSource{Monitors: []string{"10.0.0.1:6789"}, Path: "/data"}
			} else {
				pv.Spec.RBD = &corev1.RBDPersistentVolumeSource{CephMonitors: []string{"10.0.0.1:6789"}, RBDImage: "data"}
			}
			c.volumes = map[string]corev1.PersistentVolume{"/api/v1/persistentvolumes/legacy-pv": pv}
			assertStorageRefusal(t, r, c, path, plugin)
		})
	}
}

func TestRecoveryOfPreviouslySavedLegacyVolumePlanRemainsAvailable(t *testing.T) {
	for plugin, volume := range legacyInlineFixtures() {
		t.Run(plugin, func(t *testing.T) {
			r, c, path := cutoverTest(t, "exclusive", true)
			if err := r.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			p, err := loadPlan(path)
			if err != nil {
				t.Fatal(err)
			}
			object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&volume)
			if err != nil {
				t.Fatal(err)
			}
			// Reconstruct a completed migration performed by an earlier binary
			// on a server where this plugin worked. Recovery must still stop
			// the destination before attempting the original source restoration.
			for _, source := range []*unstructured.Unstructured{p.Source, c.source} {
				if err = unstructured.SetNestedSlice(source.Object, []interface{}{object}, "spec", "template", "spec", "volumes"); err != nil {
					t.Fatal(err)
				}
			}
			p.Desired.Spec.Template.Spec.Volumes = []corev1.Volume{volume}
			c.destination.Spec.Template.Spec.Volumes = []corev1.Volume{volume}
			p.Progress.AdmittedSpec.Template.Spec.Volumes = []corev1.Volume{volume}
			c.destination.Annotations[planAnnotation] = planDigest(p)
			c.hpas[0].Annotations[planAnnotation] = planDigest(p)
			if err = savePlan(path, p); err != nil {
				t.Fatal(err)
			}
			r.Options.Mode = "rollback"
			if err = r.Run(context.Background()); err != nil {
				t.Fatal("new storage guard prevented recovery of an old plan", err)
			}
			if !reflect.DeepEqual(c.source.Object["spec"], p.Source.Object["spec"]) || *c.destination.Spec.Replicas != 0 || !c.destination.Spec.Paused || c.hpas[0].Spec.ScaleTargetRef.Kind != "DeploymentConfig" {
				t.Fatal("old-plan recovery did not restore source and autoscaler")
			}
		})
	}
}

func assertStorageRefusal(t *testing.T, r *Runner, c *contractCluster, path, plugin string) {
	t.Helper()
	before := c.source.DeepCopy()
	hpa := c.hpas[0].DeepCopy()
	destination := c.destination.DeepCopy()
	err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), plugin) {
		t.Fatalf("unsafe storage accepted or wrong refusal: %v", err)
	}
	p, err := loadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.mutations) != 0 || !reflect.DeepEqual(c.source, before) || !reflect.DeepEqual(c.hpas[0], *hpa) || !reflect.DeepEqual(c.destination, destination) || p.Progress.Phase != "staged" {
		t.Fatal("source/HPA/destination changed before storage refusal", c.mutations)
	}
	if c.podDryRuns == 0 || p.Progress.Constraints == nil || p.Progress.Constraints.Status != "blocked" {
		t.Fatal("admitted Pod storage was not assessed")
	}
}

func TestSupportedStorageStillPassesPreCutoverChecks(t *testing.T) {
	for _, kind := range []string{"emptyDir", "CSI-PVC"} {
		t.Run(kind, func(t *testing.T) {
			r, c, _ := cutoverTest(t, "exclusive", true)
			volume := corev1.Volume{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}
			if kind == "CSI-PVC" {
				volume.VolumeSource = corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}
				c.claims = map[string]corev1.PersistentVolumeClaim{"/api/v1/namespaces/example/persistentvolumeclaims/data": {ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "example"}, Spec: corev1.PersistentVolumeClaimSpec{VolumeName: "csi-pv"}, Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound}}}
				c.volumes = map[string]corev1.PersistentVolume{"/api/v1/persistentvolumes/csi-pv": {ObjectMeta: metav1.ObjectMeta{Name: "csi-pv"}, Spec: corev1.PersistentVolumeSpec{PersistentVolumeSource: corev1.PersistentVolumeSource{CSI: &corev1.CSIPersistentVolumeSource{Driver: "ebs.csi.aws.com", VolumeHandle: "fixture-only"}}}}}
			}
			c.injectedPodVolumes = []corev1.Volume{volume}
			if err := r.Run(context.Background()); err != nil {
				t.Fatal("supported volume rejected", err)
			}
			if !r.Report.Results[0].Ready || r.Report.Results[0].Constraints.Status != "passed" {
				t.Fatal("supported-volume cutover did not finish")
			}
			r.Options.Mode = "rollback"
			if err := r.Run(context.Background()); err != nil {
				t.Fatal("supported-volume recovery failed", err)
			}
		})
	}
}

func TestPVTopologyLabelsCannotBypassPreRetirementChecks(t *testing.T) {
	for _, key := range []string{"failure-domain.beta.kubernetes.io/zone", "failure-domain.beta.kubernetes.io/region", "topology.kubernetes.io/zone", "topology.kubernetes.io/region"} {
		for _, value := range []string{"zone-a", "zone-a__zone-b", "", "zone-a+invalid"} {
			for _, mode := range []string{"preflight", "cutover"} {
				t.Run(key+"/"+value+"/"+mode, func(t *testing.T) {
					r, c, path := cutoverTest(t, "exclusive", true)
					r.Options.Mode = mode
					c.injectedPodVolumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}}
					c.claims = map[string]corev1.PersistentVolumeClaim{"/api/v1/namespaces/example/persistentvolumeclaims/data": {ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "example"}, Spec: corev1.PersistentVolumeClaimSpec{VolumeName: "zoned-pv"}, Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound}}}
					c.volumes = map[string]corev1.PersistentVolume{"/api/v1/persistentvolumes/zoned-pv": {ObjectMeta: metav1.ObjectMeta{Name: "zoned-pv", Labels: map[string]string{key: value}}, Spec: corev1.PersistentVolumeSpec{PersistentVolumeSource: corev1.PersistentVolumeSource{CSI: &corev1.CSIPersistentVolumeSource{Driver: "ebs.csi.aws.com", VolumeHandle: "fixture-only"}}}}}
					// Source-zone capacity is exhausted; another zone has space.
					// Ignoring the PV's independent labels would retire the source.
					for _, zone := range []string{"zone-a", "zone-c"} {
						capacity := "0"
						if zone == "zone-c" {
							capacity = "100"
						}
						c.constraintNodes = append(c.constraintNodes, corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: zone, Labels: map[string]string{key: zone}}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}, Allocatable: corev1.ResourceList{"pods": resource.MustParse(capacity)}}})
					}
					assertStorageRefusal(t, r, c, path, "topology label")
				})
			}
		}
	}
}
