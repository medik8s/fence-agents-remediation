package safetycontract

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/medik8s/fence-agents-remediation/v5/internal/controller"
	"github.com/medik8s/fence-agents-remediation/v5/pkg/utils"
)

type updateClient struct {
	client.Client
	before func(client.Object) error
}

func (c updateClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if err := c.before(obj); err != nil {
		return err
	}
	return c.Client.Update(ctx, obj, opts...)
}

type identityReader struct {
	client.Reader
	mode  string
	calls *atomic.Int32
}

func (r identityReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if nodes, ok := list.(*corev1.NodeList); ok {
		r.calls.Add(1)
		if len(opts) != 0 {
			return fmt.Errorf("identity List must not use selectors")
		}
		if r.mode == "forbidden" {
			return apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", fmt.Errorf("list denied"))
		}
		if err := r.Reader.List(ctx, nodes); err != nil {
			return err
		}
		switch r.mode {
		case "missing":
			nodes.Items = nil
		case "partial":
			nodes.Continue = "remaining-nodes"
		case "remaining-count":
			remaining := int64(1)
			nodes.RemainingItemCount = &remaining
		case "replacement":
			for i := range nodes.Items {
				if nodes.Items[i].Name == "worker-a" {
					nodes.Items[i].UID = "listed-replacement"
				}
			}
		}
		return nil
	}
	return r.Reader.List(ctx, list, opts...)
}

func TestRetryIntervalSurvivesRestart(t *testing.T) {
	h := newHarness(t, 2, nil)
	far, config, secret := safetyFixture(t)
	far.Spec.RetryCount = 2
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	far.Spec.SharedParameters["--ip"] = host
	far.Spec.SharedParameters["--ipport"] = port
	config.CA = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	current := h.getFAR(t)
	current.Spec = far.Spec
	if err := h.client.Update(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	secret.ResourceVersion = ""
	if err := h.client.Create(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	h.client = interceptor.NewClient(h.client.(client.WithWatch), interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, name string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			data, err := json.Marshal(obj)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, obj); err != nil {
				return err
			}
			return c.SubResource(name).Update(ctx, obj, opts...)
		},
	})
	h.reconciler.Client = h.client
	h.reconciler.Safety = config
	h.reconciler.APIReader = h.client
	for range 2 {
		h.reconcile(t)
	}
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 900*time.Millisecond)))
	attemptedAt := time.Now()
	h.reconcile(t)
	if h.getFAR(t).Status.SafetyAttempts != 1 || requests.Load() != 1 {
		t.Fatal("first reserved attempt control failed")
	}
	h.reconciler = &controller.FenceAgentsRemediationReconciler{Client: h.client, APIReader: h.client, Safety: config, Log: h.reconciler.Log}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}
	result, err := h.reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter <= 0 || requests.Load() != 1 || h.getFAR(t).Status.SafetyAttempts != 1 {
		t.Fatal("restart ignored persisted retry interval")
	}
	if deadline := time.Now().Add(result.RequeueAfter); deadline.Before(attemptedAt.Add(far.Spec.RetryInterval.Duration)) {
		t.Errorf("serialized retry deadline %s precedes full interval after %s", deadline, attemptedAt)
	}
	early := attemptedAt.Truncate(time.Second).Add(far.Spec.RetryInterval.Duration + 50*time.Millisecond)
	time.Sleep(time.Until(early))
	if time.Since(attemptedAt) >= far.Spec.RetryInterval.Duration {
		t.Fatal("early retry control missed its observation window")
	}
	result, err = h.reconciler.Reconcile(context.Background(), req)
	if err != nil || result.RequeueAfter <= 0 || requests.Load() != 1 || h.getFAR(t).Status.SafetyAttempts != 1 {
		t.Errorf("wire round-trip allowed an early retry: requests=%d attempts=%d error=%v", requests.Load(), h.getFAR(t).Status.SafetyAttempts, err)
	}
	next := h.getFAR(t).Status.SafetyLastAttempt.Add(far.Spec.RetryInterval.Duration + 10*time.Millisecond)
	time.Sleep(time.Until(next))
	_, err = h.reconciler.Reconcile(context.Background(), req)
	if err == nil || requests.Load() != 2 || h.getFAR(t).Status.SafetyAttempts != 2 {
		t.Fatal("elapsed retry interval did not permit the next reserved attempt")
	}
}

func TestGuardedController(t *testing.T) {
	for _, tc := range []struct {
		name, mutation, nodeList                         string
		alreadyOff, wrongUUID, stillOn, casRace, restart bool
		wantRelease                                      bool
	}{
		{name: "matching identity Off", wantRelease: true},
		{name: "unique unfiltered Node list", nodeList: "unique", wantRelease: true},
		{name: "duplicate known UUID", nodeList: "duplicate"},
		{name: "duplicate uppercase known UUID", nodeList: "uppercase-duplicate"},
		{name: "Node list forbidden", nodeList: "forbidden"},
		{name: "Node list incomplete", nodeList: "partial"},
		{name: "Node list has remaining items", nodeList: "remaining-count"},
		{name: "bound target missing from list", nodeList: "missing"},
		{name: "bound target replaced in list", nodeList: "replacement"},
		{name: "matching already Off", alreadyOff: true, wantRelease: true},
		{name: "restart with proof rechecks", restart: true, wantRelease: true},
		{name: "wrong BMC UUID", wrongUUID: true},
		{name: "accepted but On", stillOn: true},
		{name: "Unicode-aliased BMC power", mutation: "unicode-power"},
		{name: "Node replaced during precheck", mutation: "node-before-reset"},
		{name: "Node replaced during final identity GET", mutation: "node-during-final-identity"},
		{name: "duplicate UUID during final identity GET", mutation: "duplicate-during-final-identity"},
		{name: "duplicate UUID before release", mutation: "duplicate-before-release"},
		{name: "duplicate UUID during fresh Off", mutation: "duplicate-during-release"},
		{name: "Node replaced after reset", mutation: "node-after-reset"},
		{name: "Node replaced before release", mutation: "node-before-release"},
		{name: "Node replaced during fresh Off", mutation: "node-during-release"},
		{name: "Secret version changed before reset", mutation: "secret-before-reset"},
		{name: "Secret version changed before release", mutation: "secret-before-release"},
		{name: "spec target changed before reset", mutation: "target-before-reset"},
		{name: "spec target changed before release", mutation: "target-before-release"},
		{name: "CAS conflicts with replacement", casRace: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, 1, nil)
			far, config, secret := safetyFixture(t)
			var resets, reads atomic.Int32
			mutate := func(kind string) {
				switch kind {
				case "node":
					node := h.node(t)
					if node.UID == "node-b" {
						return
					}
					if err := h.client.Delete(context.Background(), node); err != nil {
						t.Error(err)
						return
					}
					node.UID = "node-b"
					node.ResourceVersion = ""
					node.Spec.Taints = nil
					if err := h.client.Create(context.Background(), node); err != nil {
						t.Error(err)
					}
				case "duplicate":
					other := h.node(t).DeepCopy()
					other.Name = "worker-b"
					other.UID = "node-b"
					other.ResourceVersion = ""
					if err := h.client.Create(context.Background(), other); err != nil {
						t.Error(err)
					}
				case "secret":
					current := &corev1.Secret{}
					if err := h.client.Get(context.Background(), client.ObjectKeyFromObject(secret), current); err != nil {
						t.Error(err)
						return
					}
					current.Data["password"] = []byte("changed")
					if err := h.client.Update(context.Background(), current); err != nil {
						t.Error(err)
					}
				case "target":
					current := h.getFAR(t)
					current.Spec.SharedParameters["--ip"] = "other.example"
					if err := h.client.Update(context.Background(), current); err != nil {
						t.Error(err)
					}
				}
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					resets.Add(1)
					if tc.mutation == "node-after-reset" {
						mutate("node")
					}
					w.WriteHeader(http.StatusAccepted)
					return
				}
				count := reads.Add(1)
				if count == 1 {
					switch tc.mutation {
					case "node-before-reset":
						mutate("node")
					case "secret-before-reset":
						mutate("secret")
					case "target-before-reset":
						mutate("target")
					}
				}
				if count == 2 && tc.mutation == "node-during-final-identity" {
					mutate("node")
				}
				if count == 2 && tc.mutation == "duplicate-during-final-identity" {
					mutate("duplicate")
				}
				if count == 4 && tc.mutation == "node-during-release" {
					mutate("node")
				}
				if count == 4 && tc.mutation == "duplicate-during-release" {
					mutate("duplicate")
				}
				uuid, power := systemUUID, "On"
				if tc.wrongUUID {
					uuid = "aabbccdd-eeff-4011-8233-445566778899"
				}
				if tc.alreadyOff || resets.Load() > 0 && !tc.stillOn {
					power = "Off"
				}
				if tc.mutation == "unicode-power" {
					_, _ = w.Write([]byte(`{"UUID":"` + systemUUID + `","PowerState":"On","Powerſtate":"Off","Actions":{"#ComputerSystem.Reset":{"target":"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset"}}}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"UUID": uuid, "PowerState": power, "Actions": map[string]any{"#ComputerSystem.Reset": map[string]string{"target": "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset"}}})
			}))
			defer server.Close()
			parsed, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			host, port, err := net.SplitHostPort(parsed.Host)
			if err != nil {
				t.Fatal(err)
			}
			far.Spec.SharedParameters["--ip"] = host
			far.Spec.SharedParameters["--ipport"] = port
			config.CA = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			current := h.getFAR(t)
			current.Spec = far.Spec
			current.Annotations = far.Annotations
			if err := h.client.Update(context.Background(), current); err != nil {
				t.Fatal(err)
			}
			secret.ResourceVersion = ""
			if err := h.client.Create(context.Background(), secret); err != nil {
				t.Fatal(err)
			}
			h.reconciler.Safety = config
			h.reconciler.APIReader = h.client
			var listCalls atomic.Int32
			if tc.nodeList != "" {
				if tc.nodeList == "duplicate" || tc.nodeList == "uppercase-duplicate" {
					other := h.node(t).DeepCopy()
					other.Name = "worker-b"
					other.UID = "node-b"
					other.ResourceVersion = ""
					if tc.nodeList == "uppercase-duplicate" {
						other.Status.NodeInfo.SystemUUID = strings.ToUpper(systemUUID)
					}
					if err := h.client.Create(context.Background(), other); err != nil {
						t.Fatal(err)
					}
				}
				h.reconciler.APIReader = identityReader{Reader: h.client, mode: tc.nodeList, calls: &listCalls}
			}
			if tc.casRace {
				h.reconciler.Client = updateClient{Client: h.client, before: func(obj client.Object) error {
					if node, ok := obj.(*corev1.Node); ok && hasOOS(node) {
						mutate("node")
						return apierrors.NewConflict(schema.GroupResource{Resource: "nodes"}, node.Name, fmt.Errorf("concurrent replacement"))
					}
					return nil
				}}
			}
			var lastErr error
			for range 8 {
				_, lastErr = h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)})
				state := h.getFAR(t)
				if state.Status.OffProof != nil && !hasOOS(h.node(t)) {
					switch tc.mutation {
					case "node-before-release":
						mutate("node")
					case "duplicate-before-release":
						mutate("duplicate")
					case "secret-before-release":
						mutate("secret")
					case "target-before-release":
						mutate("target")
					}
					if tc.restart {
						h.reconciler = &controller.FenceAgentsRemediationReconciler{Client: h.client, APIReader: h.client, Safety: config, Log: h.reconciler.Log, Recorder: h.reconciler.Recorder}
					}
				}
				if lastErr != nil || hasOOS(h.node(t)) {
					break
				}
			}
			if tc.nodeList != "" && listCalls.Load() == 0 {
				t.Error("identity did not use an uncached unfiltered Node list")
			}
			if tc.nodeList != "" && tc.nodeList != "unique" && resets.Load() != 0 {
				t.Error("ambiguous or unavailable Node inventory permitted a power action")
			}
			if hasOOS(h.node(t)) != tc.wantRelease {
				t.Fatalf("released=%t wanted=%t error=%v reads=%d resets=%d", hasOOS(h.node(t)), tc.wantRelease, lastErr, reads.Load(), resets.Load())
			}
			if tc.wantRelease {
				state := h.getFAR(t)
				if state.Status.OffProof == nil || state.Status.SafetySnapshot == nil || state.Status.OffProof.Snapshot != *state.Status.SafetySnapshot || !meta.IsStatusConditionTrue(state.Status.Conditions, utils.FenceAgentActionSucceededType) {
					t.Fatal("release lacks identity-bound proof")
				}
				if tc.alreadyOff && resets.Load() != 0 {
					t.Fatal("already-Off reset")
				}
				if tc.restart && reads.Load() < 4 {
					t.Fatal("restart did not fresh-check BMC")
				}
			} else if lastErr == nil {
				t.Fatal("negative control did not hit refusal")
			}
			if tc.mutation == "unicode-power" {
				if lastErr == nil || !strings.Contains(lastErr.Error(), "ambiguous Redfish response") {
					t.Errorf("Unicode alias refusal is not a parser rejection: %v", lastErr)
				}
				if resets.Load() != 0 {
					t.Errorf("Unicode alias permitted ForceOff: %d", resets.Load())
				}
			}
			if tc.mutation == "node-before-reset" || tc.mutation == "node-during-final-identity" || tc.mutation == "duplicate-during-final-identity" || tc.mutation == "secret-before-reset" || tc.mutation == "target-before-reset" {
				if resets.Load() != 0 {
					t.Error("power action ran after a pre-action identity/configuration change")
				}
			}
			if tc.wantRelease {
				before := reads.Load()
				if _, err := h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)}); err != nil {
					t.Fatal(err)
				}
				if reads.Load() != before {
					t.Error("terminal success generated another BMC operation")
				}
			}
			if tc.wrongUUID {
				before := h.getFAR(t).ResourceVersion
				_, _ = h.reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(h.far)})
				if h.getFAR(t).ResourceVersion != before {
					t.Error("unchanged refusal generated another status event")
				}
			}
			if h.calls.Load() != 0 {
				t.Fatal("safety mode executed generic subprocess")
			}
		})
	}
}
