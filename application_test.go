package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"strconv"
	"time"
)

type contractCluster struct {
	mutateResponse                func(*http.Request, map[string]interface{})
	inspectRequest                func(*http.Request, []byte)
	deniedLocalSCCReviewAccess    bool
	rejectLocalSCCReviewRequest   bool
	omitLocalSCCReviewAPI         bool
	localSCCReviewStatus          *authorizationv1.SubjectAccessReviewStatus
	localSCCReviewCalls           int
	reviewedSCC                   string
	podSCCOverride                *string
	dropRequiredSCC               bool
	mutateSCCSpec                 func(map[string]interface{})
	mutatePodSpec                 func(map[string]interface{})
	injectedPodVolumes            []corev1.Volume
	claims                        map[string]corev1.PersistentVolumeClaim
	volumes                       map[string]corev1.PersistentVolume
	disruptionBudgets             []policyv1.PodDisruptionBudget
	hpaStatusChurn                bool
	dropPodLabel, injectPodBudget bool
	networkPolicies               []networkingv1.NetworkPolicy
	adminPolicyVersion            string
	adminPolicies                 map[string][]unstructured.Unstructured
	clusterQuotas                 []unstructured.Unstructured
	denyPodDryRun                 bool
	podDryRuns                    int
	quotas                        []corev1.ResourceQuota
	constraintNodes               []corev1.Node
	denyConstraintRead            string

	extraReplicaSet                                                                   *unstructured.Unstructured
	sourceReplicas                                                                    int64
	orphanLabels                                                                      map[string]string
	mu                                                                                sync.Mutex
	services                                                                          []corev1.Service
	hpas                                                                              []autoscalingv2.HorizontalPodAutoscaler
	failRollout, pendingRollout, loseConnection, lost, blockRetirement, expectOverlap bool
	cancelOnActivate                                                                  context.CancelFunc
	mutations                                                                         []string
	source                                                                            *unstructured.Unstructured
	destination                                                                       *appsv1.Deployment
	creates, dryRuns                                                                  int
	denyCreate, mutateAdmission, conflict                                             bool
}

func (c *contractCluster) serve(t *testing.T, w http.ResponseWriter, req *http.Request) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inspectRequest != nil && req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		c.inspectRequest(req, body)
	}
	if c.mutateResponse != nil {
		output := w
		recorder := httptest.NewRecorder()
		w = recorder
		defer func() {
			for key, values := range recorder.Header() {
				output.Header()[key] = values
			}
			output.WriteHeader(recorder.Code)
			var object map[string]interface{}
			if recorder.Code >= 200 && recorder.Code < 300 && json.Unmarshal(recorder.Body.Bytes(), &object) == nil {
				c.mutateResponse(req, object)
				_ = json.NewEncoder(output).Encode(object)
			} else {
				_, _ = output.Write(recorder.Body.Bytes())
			}
		}()
	}
	if c.lost {
		http.Error(w, "connection lost", 503)
		return
	}
	if strings.HasPrefix(req.URL.Path, "/apis/policy.networking.k8s.io") {
		if c.adminPolicyVersion == "" {
			http.NotFound(w, req)
			return
		}
		if c.denyConstraintRead != "" && strings.HasSuffix(req.URL.Path, c.denyConstraintRead) {
			http.Error(w, "admin policy read forbidden", http.StatusForbidden)
			return
		}
		group := "policy.networking.k8s.io"
		version := group + "/" + c.adminPolicyVersion
		if req.URL.Path == "/apis/"+group {
			jsonResponse(w, map[string]interface{}{"kind": "APIGroup", "apiVersion": "v1", "name": group, "versions": []interface{}{map[string]interface{}{"groupVersion": version, "version": c.adminPolicyVersion}}})
			return
		}
		if req.URL.Path == "/apis/"+version {
			jsonResponse(w, map[string]interface{}{"kind": "APIResourceList", "apiVersion": "v1", "groupVersion": version, "resources": []interface{}{map[string]interface{}{"name": "adminnetworkpolicies", "kind": "AdminNetworkPolicy"}, map[string]interface{}{"name": "baselineadminnetworkpolicies", "kind": "BaselineAdminNetworkPolicy"}}})
			return
		}
		resource := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
		items := []interface{}{}
		for _, p := range c.adminPolicies[resource] {
			items = append(items, p.Object)
		}
		jsonResponse(w, map[string]interface{}{"kind": "List", "apiVersion": "v1", "items": items})
		return
	}
	if req.Method == http.MethodGet && req.URL.Path == "/api/v1/namespaces/example" {
		if c.denyConstraintRead == "namespace" {
			http.Error(w, "namespace read forbidden", http.StatusForbidden)
			return
		}
		jsonResponse(w, map[string]interface{}{"kind": "Namespace", "apiVersion": "v1", "metadata": map[string]interface{}{"name": "example", "labels": map[string]interface{}{"kubernetes.io/metadata.name": "example"}}})
		return
	}
	if req.Header.Get("Accept") != "application/json" {
		t.Errorf("non JSON negotiation: %s", req.Header.Get("Accept"))
	}
	if strings.HasSuffix(req.URL.Path, "selfsubjectaccessreviews") {
		var review authorizationv1.SelfSubjectAccessReview
		_ = json.NewDecoder(req.Body).Decode(&review)
		allowed := !c.deniedLocalSCCReviewAccess || review.Spec.ResourceAttributes == nil || review.Spec.ResourceAttributes.Resource != "localsubjectaccessreviews"
		jsonResponse(w, map[string]interface{}{"kind": "SelfSubjectAccessReview", "apiVersion": "authorization.k8s.io/v1", "status": map[string]interface{}{"allowed": allowed}})
		return
	}
	resources := map[string][]string{"/apis/apps.openshift.io/v1": {"deploymentconfigs"}, "/apis/apps/v1": {"deployments", "replicasets", "statefulsets", "daemonsets"}, "/api/v1": {"pods", "services", "replicationcontrollers", "endpoints"}, "/apis/authorization.k8s.io/v1": {"selfsubjectaccessreviews"}, "/apis/autoscaling/v2": {"horizontalpodautoscalers"}, "/apis/image.openshift.io/v1": {"imagestreamtags"}, "/apis/security.openshift.io/v1": {"podsecuritypolicysubjectreviews"}, "/apis/quota.openshift.io/v1": {"appliedclusterresourcequotas"}}
	if names, ok := resources[req.URL.Path]; ok {
		if req.URL.Path == "/apis/authorization.k8s.io/v1" && !c.omitLocalSCCReviewAPI {
			names = append(names, "localsubjectaccessreviews")
		}
		items := []interface{}{}
		for _, name := range names {
			items = append(items, map[string]interface{}{"name": name, "kind": "Object", "verbs": []string{"get", "list", "create", "update"}})
		}
		jsonResponse(w, map[string]interface{}{"kind": "APIResourceList", "apiVersion": "v1", "resources": items})
		return
	}
	if strings.HasSuffix(req.URL.Path, "/localsubjectaccessreviews") && req.Method == http.MethodPost {
		if c.rejectLocalSCCReviewRequest {
			http.Error(w, "review permission changed after access check", http.StatusForbidden)
			return
		}
		var review authorizationv1.LocalSubjectAccessReview
		_ = json.NewDecoder(req.Body).Decode(&review)
		ns := c.source.GetNamespace()
		sa, _, _ := unstructured.NestedString(c.source.Object, "spec", "template", "spec", "serviceAccountName")
		if sa == "" {
			sa = "default"
		}
		expectedGroups := []string{"system:serviceaccounts", "system:serviceaccounts:" + ns, "system:authenticated"}
		attributes := review.Spec.ResourceAttributes
		if review.Namespace != ns || review.Spec.User != "system:serviceaccount:"+ns+":"+sa || strings.Join(review.Spec.Groups, ",") != strings.Join(expectedGroups, ",") || attributes == nil || attributes.Namespace != ns || attributes.Group != "security.openshift.io" || attributes.Version != "v1" || attributes.Resource != "securitycontextconstraints" || attributes.Verb != "use" || attributes.Name != c.destination.Spec.Template.Annotations["openshift.io/required-scc"] {
			t.Errorf("wrong SCC subject or resource attributes: %+v", review.Spec)
		}
		c.localSCCReviewCalls++
		review.Status.Allowed = true
		if c.localSCCReviewStatus != nil {
			review.Status = *c.localSCCReviewStatus
		}
		jsonResponse(w, &review)
		return
	}
	if req.Method == http.MethodGet && c.denyConstraintRead != "" && strings.HasSuffix(req.URL.Path, "/"+c.denyConstraintRead) {
		http.Error(w, "constraint inventory denied", 403)
		return
	}
	if req.Method == http.MethodGet {
		if claim, ok := c.claims[req.URL.Path]; ok {
			jsonResponse(w, &claim)
			return
		}
		if volume, ok := c.volumes[req.URL.Path]; ok {
			jsonResponse(w, &volume)
			return
		}
	}
	if strings.HasSuffix(req.URL.Path, "/podsecuritypolicysubjectreviews") && req.Method == http.MethodPost {
		var review map[string]interface{}
		_ = json.NewDecoder(req.Body).Decode(&review)
		template := review["spec"].(map[string]interface{})["template"]
		if c.mutateSCCSpec != nil {
			c.mutateSCCSpec(template.(map[string]interface{})["spec"].(map[string]interface{}))
		}
		name := c.reviewedSCC
		if name == "" {
			name = "restricted-v2"
		}
		jsonResponse(w, map[string]interface{}{"apiVersion": "security.openshift.io/v1", "kind": "PodSecurityPolicySubjectReview", "status": map[string]interface{}{"allowedBy": map[string]interface{}{"name": name, "kind": "SecurityContextConstraints"}, "template": map[string]interface{}{"spec": template.(map[string]interface{})["spec"]}}})
		return
	}
	if strings.HasSuffix(req.URL.Path, "/pods") && req.Method == http.MethodPost {
		if req.URL.Query().Get("dryRun") != "All" {
			t.Error("preflight persisted a Pod")
		}
		c.podDryRuns++
		if c.denyPodDryRun {
			http.Error(w, "Pod admission rejected by namespace policy", 403)
			return
		}
		var pod corev1.Pod
		_ = json.NewDecoder(req.Body).Decode(&pod)
		if required := pod.Annotations["openshift.io/required-scc"]; required != "" {
			pod.Annotations["openshift.io/scc"] = required
			if c.podSCCOverride != nil {
				pod.Annotations["openshift.io/scc"] = *c.podSCCOverride
			}
			if c.dropRequiredSCC {
				delete(pod.Annotations, "openshift.io/required-scc")
			}
		}
		pod.Spec.Volumes = append(pod.Spec.Volumes, c.injectedPodVolumes...)
		if c.destination != nil {
			for key, value := range c.destination.Spec.Template.Labels {
				if pod.Labels[key] != value {
					t.Errorf("SCC review lost destination policy label %s", key)
				}
			}
		}
		object, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&pod)
		if c.dropPodLabel {
			unstructured.RemoveNestedField(object, "metadata", "labels", migrationLabel)
		}
		if c.injectPodBudget {
			_ = unstructured.SetNestedMap(object, map[string]interface{}{"requests": map[string]interface{}{"memory": "1Ti"}}, "spec", "resources")
		}
		if c.mutatePodSpec != nil {
			c.mutatePodSpec(object["spec"].(map[string]interface{}))
		}
		jsonResponse(w, object)
		return
	}
	if strings.HasSuffix(req.URL.Path, "/replicasets") && req.Method == http.MethodPost {
		if req.URL.Query().Get("dryRun") != "All" {
			t.Error("preflight persisted a ReplicaSet")
		}
		var rs appsv1.ReplicaSet
		_ = json.NewDecoder(req.Body).Decode(&rs)
		jsonResponse(w, &rs)
		return
	}
	if strings.HasSuffix(req.URL.Path, "/poddisruptionbudgets") && req.Method == http.MethodGet {
		jsonResponse(w, &policyv1.PodDisruptionBudgetList{Items: c.disruptionBudgets})
		return
	}
	if strings.HasSuffix(req.URL.Path, "/networkpolicies") && req.Method == http.MethodGet {
		jsonResponse(w, &networkingv1.NetworkPolicyList{Items: c.networkPolicies})
		return
	}
	if strings.HasSuffix(req.URL.Path, "/appliedclusterresourcequotas") && req.Method == http.MethodGet {
		jsonResponse(w, &unstructured.UnstructuredList{Object: map[string]interface{}{"kind": "AppliedClusterResourceQuotaList", "apiVersion": "quota.openshift.io/v1"}, Items: c.clusterQuotas})
		return
	}
	if strings.HasSuffix(req.URL.Path, "/resourcequotas") && req.Method == http.MethodGet {
		jsonResponse(w, &corev1.ResourceQuotaList{Items: c.quotas})
		return
	}
	if strings.HasSuffix(req.URL.Path, "/nodes") && req.Method == http.MethodGet {
		nodes := c.constraintNodes
		if nodes == nil {
			nodes = []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "worker"}, Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100"), corev1.ResourceMemory: resource.MustParse("100Gi"), corev1.ResourcePods: resource.MustParse("1000"), corev1.ResourceEphemeralStorage: resource.MustParse("100Gi")}, Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}}
		}
		jsonResponse(w, &corev1.NodeList{Items: nodes})
		return
	}
	if strings.HasSuffix(req.URL.Path, "/deploymentconfigs/web") {
		if req.Method == http.MethodPut {
			var source unstructured.Unstructured
			if err := json.NewDecoder(req.Body).Decode(&source); err != nil {
				t.Error(err)
			}
			if source.GetResourceVersion() != c.source.GetResourceVersion() {
				http.Error(w, "conflict", 409)
				return
			}
			if req.URL.Query().Get("dryRun") == "All" {
				jsonResponse(w, source.Object)
				return
			}
			version, _ := strconv.Atoi(c.source.GetResourceVersion())
			source.SetResourceVersion(strconv.Itoa(version + 1))
			n, _, _ := unstructured.NestedInt64(source.Object, "spec", "replicas")
			paused, _, _ := unstructured.NestedBool(source.Object, "spec", "paused")
			if !paused {
				c.sourceReplicas = n
			}
			_ = unstructured.SetNestedField(source.Object, c.sourceReplicas, "status", "availableReplicas")
			c.source = &source
			c.mutations = append(c.mutations, fmt.Sprintf("source:%d", n))
		}
		jsonResponse(w, c.source.Object)
		return
	}
	if strings.HasSuffix(req.URL.Path, "/deployments/web") {
		if req.Method == http.MethodPut && c.destination != nil {
			var d appsv1.Deployment
			if err := json.NewDecoder(req.Body).Decode(&d); err != nil {
				t.Error(err)
			}
			if d.ResourceVersion != c.destination.ResourceVersion {
				http.Error(w, "conflict", 409)
				return
			}
			if req.URL.Query().Get("dryRun") == "All" {
				jsonResponse(w, &d)
				return
			}
			n := *d.Spec.Replicas
			sourceN := c.sourceReplicas
			if n > 0 && !c.expectOverlap && sourceN != 0 {
				t.Error("exclusive overlap occurred")
			}
			d.Generation++
			v, _ := strconv.Atoi(d.ResourceVersion)
			d.ResourceVersion = strconv.Itoa(v + 1)
			d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation, Replicas: n, UpdatedReplicas: n, ReadyReplicas: n, AvailableReplicas: n}
			if c.failRollout && n > 0 {
				d.Status.AvailableReplicas = 0
				d.Status.ReadyReplicas = 0
				d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Reason: "ProgressDeadlineExceeded"}}
			}
			if c.pendingRollout && n > 0 {
				d.Status.ReadyReplicas = 0
				d.Status.AvailableReplicas = 0
			}
			c.destination = &d
			c.mutations = append(c.mutations, fmt.Sprintf("destination:%d", n))
			jsonResponse(w, &d)
			if n > 0 && c.cancelOnActivate != nil {
				c.cancelOnActivate()
			}
			if n > 0 && c.loseConnection {
				c.lost = true
			}
			return
		}
		if c.destination == nil {
			http.Error(w, "not found", 404)
		} else {
			response := c.destination.DeepCopy()
			response.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
			jsonResponse(w, response)
		}
		return
	}
	if strings.HasSuffix(req.URL.Path, "/deployments") && req.Method == http.MethodPost {
		var d appsv1.Deployment
		if err := json.NewDecoder(req.Body).Decode(&d); err != nil {
			t.Error(err)
		}
		if d.Spec.Replicas == nil || *d.Spec.Replicas != 0 || !d.Spec.Paused || d.Annotations[imageTriggerAnnotation] != "" {
			t.Error("unsafe staging request")
		}
		if req.URL.Query().Get("dryRun") == "All" {
			c.dryRuns++
			if c.mutateAdmission {
				v := int32(1)
				d.Spec.Replicas = &v
			}
			jsonResponse(w, &d)
			return
		}
		if c.denyCreate {
			http.Error(w, "quota rejected", 403)
			return
		}
		if c.conflict {
			http.Error(w, "already exists", 409)
			return
		}
		d.UID = "staged-uid"
		d.ResourceVersion = "1"
		c.destination = &d
		c.creates++
		jsonResponse(w, &d)
		return
	}
	if strings.Contains(req.URL.Path, "/horizontalpodautoscalers/") {
		name := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
		for i, h := range c.hpas {
			if h.Name == name {
				if req.Method == http.MethodDelete {
					var options metav1.DeleteOptions
					_ = json.NewDecoder(req.Body).Decode(&options)
					if options.Preconditions != nil && options.Preconditions.ResourceVersion != nil && *options.Preconditions.ResourceVersion != h.ResourceVersion {
						http.Error(w, "HPA resource version conflict", 409)
						return
					}
					if len(options.DryRun) > 0 {
						jsonResponse(w, map[string]interface{}{"kind": "Status", "apiVersion": "v1", "status": "Success"})
						return
					}
					c.hpas = append(c.hpas[:i], c.hpas[i+1:]...)
					c.mutations = append(c.mutations, "hpa:delete")
					jsonResponse(w, map[string]interface{}{"kind": "Status", "apiVersion": "v1", "status": "Success"})
					return
				}
				if c.hpaStatusChurn {
					version, _ := strconv.Atoi(c.hpas[i].ResourceVersion)
					c.hpas[i].ResourceVersion = strconv.Itoa(version + 1)
				}
				h.TypeMeta = metav1.TypeMeta{APIVersion: "autoscaling/v2", Kind: "HorizontalPodAutoscaler"}
				jsonResponse(w, &h)
				return
			}
		}
		http.Error(w, "not found", 404)
		return
	}
	if strings.HasSuffix(req.URL.Path, "/horizontalpodautoscalers") && req.Method == http.MethodPost {
		var h autoscalingv2.HorizontalPodAutoscaler
		_ = json.NewDecoder(req.Body).Decode(&h)
		if req.URL.Query().Get("dryRun") == "All" {
			jsonResponse(w, &h)
			return
		}
		h.UID = "new-hpa"
		h.ResourceVersion = "2"
		c.hpas = append(c.hpas, h)
		c.mutations = append(c.mutations, "hpa:"+h.Spec.ScaleTargetRef.Kind)
		jsonResponse(w, &h)
		return
	}
	if strings.HasSuffix(req.URL.Path, "/services/web") {
		for _, svc := range c.services {
			jsonResponse(w, svc)
			return
		}
		http.NotFound(w, req)
		return
	}
	if strings.HasSuffix(req.URL.Path, "/endpoints/web") {
		uid := typesUID("source-pod")
		if c.destination != nil && *c.destination.Spec.Replicas > 0 {
			uid = typesUID("destination-pod")
		}
		jsonResponse(w, &corev1.Endpoints{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Endpoints"}, ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "example"}, Subsets: []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{UID: uid}}}}}})
		return
	}
	if req.Method == http.MethodGet {
		kind := "List"
		version := "v1"
		items := []interface{}{}
		if strings.HasSuffix(req.URL.Path, "/services") {
			kind = "ServiceList"
			for _, svc := range c.services {
				items = append(items, svc)
			}
		}
		if strings.HasSuffix(req.URL.Path, "/horizontalpodautoscalers") {
			kind = "HorizontalPodAutoscalerList"
			version = "autoscaling/v2"
			for _, h := range c.hpas {
				items = append(items, h)
			}
		}
		if strings.HasSuffix(req.URL.Path, "/pods") {
			kind = "PodList"
			selector, _ := labels.Parse(req.URL.Query().Get("labelSelector"))
			sourceN := c.sourceReplicas
			pods := []corev1.Pod{}
			if sourceN > 0 || c.blockRetirement {
				pods = append(pods, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "source-pod", Namespace: "example", UID: "source-pod", Labels: map[string]string{"deploymentconfig": "web", "app": "web", "deployment": "web-1"}}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}})
			}
			if c.orphanLabels != nil {
				pods = append(pods, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan", UID: "orphan", Labels: c.orphanLabels}})
			}
			if c.destination != nil && *c.destination.Spec.Replicas > 0 {
				pods = append(pods, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "destination-pod", Namespace: "example", UID: "destination-pod", Labels: c.destination.Spec.Template.Labels}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}})
			}
			for _, pod := range pods {
				if selector.Matches(labels.Set(pod.Labels)) {
					items = append(items, pod)
				}
			}
		}
		if strings.HasSuffix(req.URL.Path, "/deploymentconfigs") {
			kind = "DeploymentConfigList"
			version = "apps.openshift.io/v1"
			items = append(items, c.source.Object)
		}
		if strings.HasSuffix(req.URL.Path, "/replicasets") && c.extraReplicaSet != nil {
			items = append(items, c.extraReplicaSet.Object)
		}
		if strings.HasSuffix(req.URL.Path, "/deployments") && c.destination != nil {
			items = append(items, c.destination)
		}
		jsonResponse(w, map[string]interface{}{"kind": kind, "apiVersion": version, "items": items})
		return
	}
	t.Errorf("unexpected mutation %s %s", req.Method, req.URL.Path)
	http.Error(w, "unexpected", 500)
}
func typesUID(s string) types.UID { return types.UID(s) }

func stageTest(t *testing.T) (*Runner, *contractCluster, string) {
	t.Helper()
	cluster := &contractCluster{source: fixture(t), sourceReplicas: 1}
	r := liveTestRunner(t, func(w http.ResponseWriter, req *http.Request) { cluster.serve(t, w, req) })
	if err := r.connect(); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", r.Options.Kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	cfg.QPS = 1000
	cfg.Burst = 1000
	cfg.ContentType = "application/json"
	cfg.AcceptContentTypes = "application/json"
	r.Dynamic, err = dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.Kube, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.Convert(cluster.source)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.buildPlan(context.Background(), cluster.source, c, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.Options.OutputDir, "web.plan.json")
	if err = savePlan(path, p); err != nil {
		t.Fatal(err)
	}
	r.PollInterval = time.Millisecond
	r.Options.Projects = nil
	r.Options.PlanFile = path
	r.Options.Mode = "stage"
	return r, cluster, path
}
func TestStageDryRunAndRerun(t *testing.T) {
	r, c, path := stageTest(t)
	r.Options.Mode = "dry-run"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.creates != 0 || c.dryRuns != 1 {
		t.Fatal("dry run persisted a workload")
	}
	r.Options.Mode = "stage"
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.creates != 1 || r.Report.Totals.Applied != 1 {
		t.Fatalf("%+v", r.Report)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.creates != 1 {
		t.Fatal("rerun duplicated create")
	}
	p, err := loadPlan(path)
	if err != nil || p.Progress.Phase != "staged" || p.Progress.DestinationUID != "staged-uid" {
		t.Fatalf("%v %+v", err, p)
	}
}
func TestStageFailures(t *testing.T) {
	for _, scenario := range []string{"stale", "foreign", "denied", "conflict", "admission", "tampered", "wrong-context", "unsupported", "active"} {
		t.Run(scenario, func(t *testing.T) {
			r, c, path := stageTest(t)
			p, _ := loadPlan(path)
			switch scenario {
			case "stale":
				c.source.SetResourceVersion("43")
			case "foreign":
				c.destination = &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "example"}}
			case "denied":
				c.denyCreate = true
			case "conflict":
				c.conflict = true
			case "admission":
				c.mutateAdmission = true
			case "tampered":
				p.Desired.Name = "other"
				_ = savePlan(path, p)
			case "wrong-context":
				p.Context = "production"
				_ = savePlan(path, p)
			case "unsupported":
				_ = unstructured.SetNestedField(p.Source.Object, true, "spec", "test")
				_ = savePlan(path, p)
			case "active":
				if err := r.Run(context.Background()); err != nil {
					t.Fatal(err)
				}
				one := int32(1)
				c.destination.Spec.Replicas = &one
			}
			if err := r.Run(context.Background()); err == nil || r.Report.Totals.Failed != 1 {
				t.Fatalf("%s failure swallowed: %v %+v", scenario, err, r.Report)
			}
			if scenario != "active" && c.creates != 0 {
				t.Fatal("failed validation persisted workload")
			}
		})
	}
}
func TestPlanReadOnlyAndOfflineNotExecutable(t *testing.T) {
	r, c, path := stageTest(t)
	p, _ := loadPlan(path)
	if !p.InventoryComplete || c.creates != 0 {
		t.Fatal("planning wrote workload")
	}
	p.InventoryComplete = false
	_ = savePlan(path, p)
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("executed offline plan")
	}
	if c.creates != 0 {
		t.Fatal("unsafe create")
	}
	_ = fmt.Sprint(p.Recovery)
}
