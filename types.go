package main

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
