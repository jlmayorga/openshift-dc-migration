package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
)

func TestSourceConflictRetries(t *testing.T) {
	for _, stage := range []string{"dry-run", "write"} {
		for _, conflicts := range []int{1, 4, 5} {
			t.Run(fmt.Sprintf("%s-%d", stage, conflicts), func(t *testing.T) {
				r, c, path := stageTest(t)
				p, _ := loadPlan(path)
				original := sourceSpec(p, false, true)
				desired := sourceSpec(p, false, false)
				gets, dryRuns, writes, injected := 0, 0, 0, 0
				c.inspectRequest = func(req *http.Request, _ []byte) {
					if !strings.HasSuffix(req.URL.Path, "/deploymentconfigs/web") {
						return
					}
					if req.Method == http.MethodGet {
						gets++
						return
					}
					if req.Method != http.MethodPut {
						return
					}
					dry := req.URL.Query().Get("dryRun") == "All"
					if dry {
						dryRuns++
					} else {
						writes++
					}
					if dry != (stage == "dry-run") || injected >= conflicts {
						return
					}
					version, _ := strconv.Atoi(c.source.GetResourceVersion())
					c.source.SetResourceVersion(strconv.Itoa(version + 1))
					_ = unstructured.SetNestedField(c.source.Object, fmt.Sprintf("controller-status-%d", injected), "status", "details", "message")
					injected++
				}
				err := r.setSourceSpec(context.Background(), p, desired, "test source transition")
				c.mu.Lock()
				defer c.mu.Unlock()
				if conflicts == 5 {
					if err == nil || !apierrors.IsConflict(err) || !strings.Contains(err.Error(), "5 attempts") {
						t.Fatalf("retry exhaustion lost: %v", err)
					}
					if gets != 5 || dryRuns != 5 || len(c.mutations) != 0 || !reflect.DeepEqual(c.source.Object["spec"], original) {
						t.Fatalf("exhaustion mutated or unbounded: gets=%d dry=%d writes=%d mutations=%v", gets, dryRuns, writes, c.mutations)
					}
					if p.Progress.Phase != "source-intent" {
						t.Fatal(p.Progress)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if gets != conflicts+1 || dryRuns != conflicts+1 || len(c.mutations) != 1 {
					t.Fatalf("did not refetch/redryrun exactly: gets=%d dry=%d writes=%d mutations=%v", gets, dryRuns, writes, c.mutations)
				}
				if stage == "dry-run" && writes != 1 || stage == "write" && writes != conflicts+1 {
					t.Fatalf("wrong writes %d", writes)
				}
				if !reflect.DeepEqual(c.source.Object["spec"], desired) || p.Progress.Phase != "source-updated" {
					t.Fatal("source transition not confirmed")
				}
				message, _, _ := unstructured.NestedString(c.source.Object, "status", "details", "message")
				if message != fmt.Sprintf("controller-status-%d", conflicts-1) {
					t.Fatal("fresh controller status lost", message)
				}
			})
		}
	}
}

func TestSourceConflictRevalidatesIdentityAndEntireSpec(t *testing.T) {
	for _, stage := range []string{"dry-run", "write"} {
		for _, change := range []string{"uid", "deleting", "deep-spec", "another-migration-state", "desired-spec"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				r, c, path := stageTest(t)
				p, _ := loadPlan(path)
				desired := sourceSpec(p, false, false)
				attempts := 0
				c.inspectRequest = func(req *http.Request, _ []byte) {
					if req.Method != http.MethodPut || !strings.HasSuffix(req.URL.Path, "/deploymentconfigs/web") || (req.URL.Query().Get("dryRun") == "All") != (stage == "dry-run") {
						return
					}
					attempts++
					if attempts != 1 {
						return
					}
					version, _ := strconv.Atoi(c.source.GetResourceVersion())
					c.source.SetResourceVersion(strconv.Itoa(version + 1))
					switch change {
					case "uid":
						c.source.SetUID("replacement")
					case "deleting":
						now := metav1.Now()
						c.source.SetDeletionTimestamp(&now)
					case "deep-spec":
						_ = unstructured.SetNestedField(c.source.Object, "external-value", "spec", "template", "metadata", "labels", "external")
					case "another-migration-state":
						c.source.Object["spec"] = sourceSpec(p, true, false)
					case "desired-spec":
						c.source.Object["spec"] = desired
					}
				}
				err := r.setSourceSpec(context.Background(), p, desired, "test source transition")
				if err == nil {
					t.Fatal("external change accepted during conflict retry")
				}
				c.mu.Lock()
				defer c.mu.Unlock()
				if attempts != 1 || len(c.mutations) != 0 || p.Progress.Phase != "source-intent" {
					t.Fatalf("external change overwritten or retried: %d %v %v", attempts, c.mutations, p.Progress)
				}
				if (change == "deep-spec" || change == "another-migration-state" || change == "desired-spec") && !strings.Contains(err.Error(), "spec changed during conflict retry") {
					t.Fatal(err)
				}
			})
		}
	}
}

type sourceRetryRoundTripper func(*http.Request) (*http.Response, error)

func (f sourceRetryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSourceUncertainWriteIsNeverRetried(t *testing.T) {
	for _, scenario := range []string{"lost-response", "eof", "timeout", "unavailable", "timeout-retry-after"} {
		t.Run(scenario, func(t *testing.T) {
			r, c, path := stageTest(t)
			p, _ := loadPlan(path)
			cfg, err := clientcmd.BuildConfigFromFlags("", r.Options.Kubeconfig)
			if err != nil {
				t.Fatal(err)
			}
			gets, writes := 0, 0
			cfg.WrapTransport = func(transport http.RoundTripper) http.RoundTripper {
				return sourceRetryRoundTripper(func(req *http.Request) (*http.Response, error) {
					if strings.HasSuffix(req.URL.Path, "/deploymentconfigs/web") && req.Method == http.MethodGet {
						gets++
					}
					response, err := transport.RoundTrip(req)
					if err == nil && req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/deploymentconfigs/web") && req.URL.Query().Get("dryRun") == "" {
						writes++
						_, _ = io.Copy(io.Discard, response.Body)
						_ = response.Body.Close()
						switch scenario {
						case "lost-response":
							return nil, errors.New("qualification: response lost after committed source write")
						case "eof":
							return nil, io.EOF
						default:
							status, reason := http.StatusGatewayTimeout, "Timeout"
							if scenario == "unavailable" {
								status, reason = http.StatusServiceUnavailable, "ServiceUnavailable"
							}
							body := fmt.Sprintf(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":%q,"code":%d,"message":"response unavailable after committed write"}`, reason, status)
							response.StatusCode = status
							response.Status = http.StatusText(status)
							response.Body = io.NopCloser(strings.NewReader(body))
							response.ContentLength = int64(len(body))
							response.Header.Set("Content-Type", "application/json")
							response.Header.Del("Content-Length")
							if scenario == "timeout-retry-after" {
								response.Header.Set("Retry-After", "0")
							}
							return response, nil
						}
					}
					return response, err
				})
			}
			r.Dynamic, err = newJSONDynamicClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			desired := sourceSpec(p, false, false)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err = r.setSourceSpec(ctx, p, desired, "quiesce source")
			if err == nil || !strings.Contains(err.Error(), "state may be uncertain") {
				t.Fatal(err)
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if gets != 1 || writes != 1 || len(c.mutations) != 1 || !reflect.DeepEqual(c.source.Object["spec"], desired) || p.Progress.Phase != "source-intent" {
				t.Fatalf("uncertain mutation retried or success claimed: gets=%d writes=%d phase=%s", gets, writes, p.Progress.Phase)
			}
		})
	}
}

func TestSourceConflictBackoffCancellation(t *testing.T) {
	r, c, path := stageTest(t)
	p, _ := loadPlan(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	c.inspectRequest = func(req *http.Request, _ []byte) {
		if req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/deploymentconfigs/web") {
			calls++
			c.source.SetResourceVersion("conflicting-version")
		}
	}
	done := make(chan error, 1)
	go func() { done <- r.setSourceSpec(ctx, p, sourceSpec(p, false, false), "cancelled transition") }()
	deadline := time.Now().Add(time.Second)
	for {
		saved, err := loadPlan(path)
		if err == nil && strings.Contains(strings.Join(saved.Progress.Events, "\n"), "conflict; retry") {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("did not reach conflict backoff")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("cancellation not bounded: %v calls=%d", err, calls)
		}
	case <-time.After(time.Second):
		t.Fatal("conflict backoff ignored cancellation")
	}
}
