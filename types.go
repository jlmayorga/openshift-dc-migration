package main

type ConversionInfo struct {
	Timestamp            string
	Namespace            string
	DeploymentConfigName string
	HasTriggers          bool
	HasLifecycleHooks    bool
	HasAutoRollbacks     bool
	UsesCustomStrategies bool
}

var conversionInfos []ConversionInfo

var (
	dcSpecificLabels = []string{
		"openshift.io/deployment-config.name",
	}

	dcSpecificAnnotations = []string{
		"openshift.io/deployment-config.name",
		"openshift.io/deployment-config.latest-version",
		"openshift.io/deployment.phase",
	}
)
