package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

var dcResource = schema.GroupVersionResource{Group: "apps.openshift.io", Version: "v1", Resource: "deploymentconfigs"}
var deploymentResource = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

type Options struct {
	Kubeconfig, Context, OutputDir, ReportPath, PDFPath, LogPath string
	Projects, Inputs, Reserved                                   []string
	PreserveLabels, PreserveAnnotations, ApplyChanges            bool
	RequestTimeout, OperationTimeout                             time.Duration
}

func DefaultOptions() Options {
	return Options{OutputDir: "converted_deployments", ReportPath: "conversion_report.json", PDFPath: "conversion_report.pdf", PreserveLabels: true, PreserveAnnotations: true, Reserved: []string{"default", "openshift", "openshift-infra"}, RequestTimeout: 30 * time.Second, OperationTimeout: 15 * time.Minute}
}
func (o Options) Validate() error {
	if len(o.Inputs) == 0 && len(o.Projects) == 0 {
		return errors.New("specify --input or --projects")
	}
	if len(o.Inputs) > 0 && len(o.Projects) > 0 {
		return errors.New("--input and --projects are mutually exclusive")
	}
	if o.RequestTimeout <= 0 || o.OperationTimeout <= 0 {
		return errors.New("timeouts must be positive")
	}
	if o.ApplyChanges {
		return errors.New("application is disabled until controlled staging is available")
	}
	for _, p := range o.Projects {
		if len(validation.IsDNS1123Label(p)) > 0 {
			return fmt.Errorf("invalid project %q", p)
		}
	}
	return nil
}

type Identity struct {
	Namespace       string `json:"namespace,omitempty"`
	Name            string `json:"name,omitempty"`
	UID             string `json:"uid,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}
type Result struct {
	Source      Identity `json:"source"`
	Destination Identity `json:"destination"`
	Stage       string   `json:"stage"`
	Outcome     string   `json:"outcome"`
	Warnings    []string `json:"warnings,omitempty"`
	Unsupported []string `json:"unsupported,omitempty"`
	Error       string   `json:"error,omitempty"`
	Generated   bool     `json:"generated"`
	Applied     bool     `json:"applied"`
	Ready       bool     `json:"ready"`
}
type Report struct {
	SchemaVersion string   `json:"schemaVersion"`
	Timestamp     string   `json:"timestamp"`
	Context       string   `json:"context,omitempty"`
	Results       []Result `json:"results"`
}
type Runner struct {
	Options Options
	Dynamic dynamic.Interface
	Kube    kubernetes.Interface
	Logger  *log.Logger
	Out     io.Writer
	Now     func() time.Time
	Report  Report
}

func NewRunner(o Options) *Runner {
	return &Runner{Options: o, Logger: log.New(io.Discard, "", 0), Out: io.Discard, Now: time.Now}
}
func (r *Runner) isReservedNamespace(ns string) bool {
	if strings.HasPrefix(ns, "openshift-") || strings.HasPrefix(ns, "kube-") {
		return true
	}
	return contains(r.Options.Reserved, ns)
}
func (r *Runner) connect() error {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = r.Options.Kubeconfig
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: r.Options.Context})
	cfg, err := loader.ClientConfig()
	if err != nil {
		return err
	}
	raw, err := loader.RawConfig()
	if err != nil {
		return err
	}
	r.Report.Context = r.Options.Context
	if r.Report.Context == "" {
		r.Report.Context = raw.CurrentContext
	}
	cfg.Timeout = r.Options.RequestTimeout
	cfg.ContentType = "application/json"
	cfg.AcceptContentTypes = "application/json"
	r.Dynamic, err = dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	r.Kube, err = kubernetes.NewForConfig(cfg)
	return err
}
func (r *Runner) Run(ctx context.Context) error {
	r.Report = Report{SchemaVersion: "v1", Timestamp: r.Now().UTC().Format(time.RFC3339), Results: []Result{}}
	var failures []error
	if len(r.Options.Inputs) > 0 {
		for _, path := range r.Options.Inputs {
			objects, err := readDCFile(path)
			if err != nil {
				r.Report.Results = append(r.Report.Results, Result{Source: Identity{Name: path}, Stage: "input", Outcome: "failed", Error: err.Error()})
				failures = append(failures, err)
				continue
			}
			for _, dc := range objects {
				if err := r.process(ctx, dc); err != nil {
					failures = append(failures, err)
				}
			}
		}
	} else {
		if r.Dynamic == nil {
			if err := r.connect(); err != nil {
				return err
			}
		}
		seen := map[string]bool{}
		for _, ns := range r.Options.Projects {
			if seen[ns] {
				continue
			}
			seen[ns] = true
			if r.isReservedNamespace(ns) {
				r.Report.Results = append(r.Report.Results, Result{Source: Identity{Namespace: ns}, Stage: "project", Outcome: "skipped"})
				continue
			}
			list, err := r.Dynamic.Resource(dcResource).Namespace(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				r.Report.Results = append(r.Report.Results, Result{Source: Identity{Namespace: ns}, Stage: "discovery", Outcome: "failed", Error: err.Error()})
				failures = append(failures, err)
				continue
			}
			if len(list.Items) == 0 {
				r.Report.Results = append(r.Report.Results, Result{Source: Identity{Namespace: ns}, Stage: "discovery", Outcome: "empty"})
			}
			for i := range list.Items {
				if err := r.process(ctx, &list.Items[i]); err != nil {
					failures = append(failures, err)
				}
			}
		}
	}
	return errors.Join(failures...)
}
func (r *Runner) process(ctx context.Context, dc *unstructured.Unstructured) error {
	result := Result{Source: Identity{Namespace: dc.GetNamespace(), Name: dc.GetName(), UID: string(dc.GetUID()), ResourceVersion: dc.GetResourceVersion()}, Destination: Identity{Namespace: dc.GetNamespace(), Name: dc.GetName()}, Stage: "conversion", Outcome: "failed"}
	defer func() { r.Report.Results = append(r.Report.Results, result) }()
	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
		return err
	}
	conversion, err := r.Convert(dc)
	if err != nil {
		result.Error = err.Error()
		return err
	}
	result.Warnings = conversion.Warnings
	result.Unsupported = conversion.Unsupported
	dep := conversion.Deployment
	result.Stage = "save"
	data, err := yaml.Marshal(dep)
	if err == nil {
		dir := filepath.Join(r.Options.OutputDir, dc.GetNamespace())
		err = os.MkdirAll(dir, 0700)
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, dc.GetName()+".yaml"), data, 0600)
		}
	}
	if err != nil {
		result.Error = err.Error()
		return err
	}
	result.Generated = true
	result.Outcome = "generated"
	result.Stage = "generation"
	if len(result.Unsupported) > 0 {
		result.Outcome = "manual"
		result.Error = "manual migration required: " + strings.Join(result.Unsupported, "; ")
		return errors.New(result.Error)
	}
	return nil
}
func readDCFile(path string) ([]*unstructured.Unstructured, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := yamlutil.NewYAMLOrJSONDecoder(f, 4096)
	var objects []*unstructured.Unstructured
	for {
		var obj unstructured.Unstructured
		err = dec.Decode(&obj)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(obj.Object) == 0 {
			continue
		}
		if obj.IsList() {
			items, e := obj.ToList()
			if e != nil {
				return nil, e
			}
			for i := range items.Items {
				objects = append(objects, &items.Items[i])
			}
		} else {
			objects = append(objects, &obj)
		}
	}
	if len(objects) == 0 {
		return nil, errors.New("input contains no DeploymentConfigs")
	}
	return objects, nil
}
