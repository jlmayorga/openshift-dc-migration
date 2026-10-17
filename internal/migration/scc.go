package migration

import (
	"context"
	"fmt"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A subject review can select a different SCC from the workload's required
// annotation. A caller's successful Pod dry run cannot prove that its service
// account may use that required SCC. Keep this extra review namespace-scoped.
func (r *Runner) authorizeRequiredSCC(ctx context.Context, ns, serviceAccount, required string) error {
	if err := r.requireAPI(ctx, "authorization.k8s.io/v1", "localsubjectaccessreviews"); err != nil {
		return err
	}
	if err := r.access(ctx, ns, "authorization.k8s.io", "localsubjectaccessreviews", "create"); err != nil {
		return err
	}
	review := &authorizationv1.LocalSubjectAccessReview{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns},
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   "system:serviceaccount:" + ns + ":" + serviceAccount,
			Groups: []string{"system:serviceaccounts", "system:serviceaccounts:" + ns, "system:authenticated"},
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: ns, Verb: "use", Group: "security.openshift.io", Version: "v1", Resource: "securitycontextconstraints", Name: required,
			},
		},
	}
	response, err := r.Kube.AuthorizationV1().LocalSubjectAccessReviews(ns).Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("verify service account %s use of required SCC %q: %w", serviceAccount, required, err)
	}
	status := response.Status
	if !status.Allowed || status.Denied || status.EvaluationError != "" {
		return fmt.Errorf("service account %s cannot be verified to use required SCC %q: allowed=%t denied=%t reason=%s evaluationError=%s", serviceAccount, required, status.Allowed, status.Denied, status.Reason, status.EvaluationError)
	}
	return nil
}
