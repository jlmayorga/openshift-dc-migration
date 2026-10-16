package main

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// API acceptance does not establish kubelet plugin support. These baseline
// fields remain decodable after their drivers were removed or disabled.
func legacyStorageDiagnostic(plugin string) string {
	switch plugin {
	case "cephfs", "rbd":
		return plugin + " in-tree storage, removed in Kubernetes 1.31; migrate the data and workload to a reviewed CSI volume before automatic migration"
	case "gitRepo":
		return "gitRepo storage, disabled by default in Kubernetes 1.33; replace it with a reviewed init-container checkout into an emptyDir before automatic migration"
	default:
		return ""
	}
}

func unsupportedInlineVolumes(volumes []corev1.Volume) []string {
	var problems []string
	for _, volume := range volumes {
		plugin := ""
		switch {
		case volume.CephFS != nil:
			plugin = "cephfs"
		case volume.RBD != nil:
			plugin = "rbd"
		case volume.GitRepo != nil:
			plugin = "gitRepo"
		}
		if problem := legacyStorageDiagnostic(plugin); problem != "" {
			problems = append(problems, fmt.Sprintf("volume %q uses %s", volume.Name, problem))
		}
	}
	return problems
}

func checkLegacyPV(pv *corev1.PersistentVolume) error {
	plugin := ""
	switch {
	case pv.Spec.CephFS != nil:
		plugin = "cephfs"
	case pv.Spec.RBD != nil:
		plugin = "rbd"
	}
	if problem := legacyStorageDiagnostic(plugin); problem != "" {
		return fmt.Errorf("PV %q uses %s", pv.Name, problem)
	}
	return nil
}
