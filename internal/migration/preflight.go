package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *Runner) discover(ctx context.Context, groupVersion string) (*metav1.APIResourceList, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Options.RequestTimeout)
	defer cancel()
	if r.Discover != nil {
		return r.Discover(ctx, groupVersion)
	}
	if r.Kube == nil || r.Kube.Discovery().RESTClient() == nil {
		return nil, errors.New("discovery client is required")
	}
	path := "/apis/" + groupVersion
	if groupVersion == "v1" {
		path = "/api/v1"
	}
	var list metav1.APIResourceList
	err := r.Kube.Discovery().RESTClient().Get().AbsPath(path).Do(ctx).Into(&list)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("API %s is absent or disabled; use --input with an exported DC for offline conversion: %w", groupVersion, err)
		}
		return nil, fmt.Errorf("discover %s (permission, authentication or transport failure): %w", groupVersion, err)
	}
	return &list, nil
}
func (r *Runner) requireAPI(ctx context.Context, gv, resource string) error {
	list, err := r.discover(ctx, gv)
	if err != nil {
		return err
	}
	for _, v := range list.APIResources {
		if v.Name == resource {
			return nil
		}
	}
	return fmt.Errorf("API %s resource %s unavailable; exported DCs can be converted using --input", gv, resource)
}
func (r *Runner) preflight(ctx context.Context) error {
	for _, pair := range [][2]string{{"apps.openshift.io/v1", "deploymentconfigs"}, {"apps/v1", "deployments"}, {"v1", "pods"}, {"authorization.k8s.io/v1", "selfsubjectaccessreviews"}} {
		if err := r.requireAPI(ctx, pair[0], pair[1]); err != nil {
			return err
		}
	}
	return nil
}
func (r *Runner) access(ctx context.Context, ns, group, resource, verb string) error {
	if r.Kube == nil {
		return errors.New("authorization client is required")
	}
	ctx, cancel := context.WithTimeout(ctx, r.Options.RequestTimeout)
	defer cancel()
	// Review the same version used by the real request instead of relying on
	// the server's empty/wildcard default, which changed in Kubernetes 1.28.
	version := "v1"
	switch group {
	case "autoscaling":
		version = "v2"
	case adminPolicyGroup:
		version = adminPolicyVersion
	}
	review := &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: ns, Group: group, Version: version, Resource: resource, Verb: verb}}}
	response, err := r.Kube.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("check %s %s in %s: %w", verb, resource, ns, err)
	}
	if !response.Status.Allowed {
		return fmt.Errorf("denied %s %s in %s: %s %s", verb, resource, ns, response.Status.Reason, response.Status.EvaluationError)
	}
	return nil
}
func normalizeProjects(projects []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, p := range projects {
		if !seen[p] {
			result = append(result, p)
			seen[p] = true
		}
	}
	return result
}
func actionableError(err error) string {
	if apierrors.IsNotFound(err) {
		return "DeploymentConfig API or requested resource unavailable; use an exported DC with --input. " + err.Error()
	}
	if strings.Contains(err.Error(), "context deadline") {
		return "request deadline exceeded: " + err.Error()
	}
	return err.Error()
}
