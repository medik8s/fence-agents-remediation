package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	farapi "github.com/medik8s/fence-agents-remediation/v5/api/v1alpha1"
)

func TestDirectCredentialNamespace(t *testing.T) {
	var mutex sync.Mutex
	var paths []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		paths = append(paths, r.URL.Path)
		mutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/namespaces/bmc/secrets/credentials" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]string{"name": "credentials", "namespace": "bmc", "uid": "secret-a", "resourceVersion": "1"}, "data": map[string][]byte{"username": []byte("user"), "password": []byte("password")}})
	}))
	defer server.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{corev1.SchemeGroupVersion})
	mapper.Add(corev1.SchemeGroupVersion.WithKind("Secret"), meta.RESTScopeNamespace)
	reader, err := client.New(&rest.Config{Host: server.URL, TLSClientConfig: rest.TLSClientConfig{CAData: ca}}, client.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		t.Fatal(err)
	}
	name := "credentials"
	far := &farapi.FenceAgentsRemediation{ObjectMeta: metav1.ObjectMeta{Namespace: "bmc"}, Spec: farapi.FenceAgentsRemediationSpec{SafetyPolicy: farapi.RedfishOffV1, NodeBinding: &farapi.NodeBinding{Version: "v1", Name: "worker-a", UID: "node-a", SystemUUID: "00112233-4455-4677-8899-aabbccddeeff"}, Agent: "fence_redfish", RetryCount: 1, RetryInterval: metav1.Duration{Duration: time.Second}, Timeout: metav1.Duration{Duration: time.Second}, RemediationStrategy: farapi.OutOfServiceTaintRemediationStrategy, SharedSecretName: &name, SharedParameters: map[farapi.ParameterName]string{"--action": "off", "--ip": "bmc.example", "--systems-uri": "/redfish/v1/Systems/1"}}}
	config := farapi.SafetyConfig{Enabled: true, CredentialNamespace: "bmc", TrustedNHCCreator: "system:serviceaccount:operators:nhc", CA: ca}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := farapi.ResolveSafetyTarget(ctx, reader, far, config); err != nil {
		t.Fatal(err)
	}
	far.Namespace = "foreign"
	if _, err := farapi.ResolveSafetyTarget(ctx, reader, far, config); err == nil {
		t.Fatal("foreign namespace accepted")
	}
	far.Namespace = "bmc"
	name = "../foreign/secrets/credentials"
	if _, err := farapi.ResolveSafetyTarget(ctx, reader, far, config); err == nil {
		t.Fatal("path-shaped Secret name accepted")
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(paths) != 1 || paths[0] != "/api/v1/namespaces/bmc/secrets/credentials" {
		t.Fatalf("direct reader paths=%v", paths)
	}
}

func TestNamespacedResourceCacheScope(t *testing.T) {
	resources := []struct {
		plural, kind string
		version      schema.GroupVersion
		object       client.Object
		list         client.ObjectList
	}{
		{"secrets", "Secret", corev1.SchemeGroupVersion, &corev1.Secret{}, &corev1.SecretList{}},
		{"fenceagentsremediations", "FenceAgentsRemediation", farapi.GroupVersion, &farapi.FenceAgentsRemediation{}, &farapi.FenceAgentsRemediationList{}},
		{"fenceagentsremediationtemplates", "FenceAgentsRemediationTemplate", farapi.GroupVersion, &farapi.FenceAgentsRemediationTemplate{}, &farapi.FenceAgentsRemediationTemplateList{}},
	}
	for _, resource := range resources {
		for _, enabled := range []bool{false, true} {
			mode := "default"
			if enabled {
				mode = "safety"
			}
			t.Run(resource.plural+"/"+mode, func(t *testing.T) {
				base := "/apis/" + resource.version.String()
				if resource.version.Group == "" {
					base = "/api/" + resource.version.Version
				}
				globalPath := base + "/" + resource.plural
				scopedPath := base + "/namespaces/bmc/" + resource.plural
				var mutex sync.Mutex
				var paths []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mutex.Lock()
					paths = append(paths, r.URL.Path)
					mutex.Unlock()
					w.Header().Set("Content-Type", "application/json")
					namespace := "foreign"
					if enabled {
						namespace = "bmc"
					}
					object := map[string]any{"apiVersion": resource.version.String(), "kind": resource.kind, "metadata": map[string]string{"name": "fixture", "namespace": namespace, "resourceVersion": "1"}}
					if r.URL.Query().Get("watch") == "true" {
						w.WriteHeader(http.StatusOK)
						if r.URL.Query().Get("sendInitialEvents") == "true" {
							encoder := json.NewEncoder(w)
							_ = encoder.Encode(map[string]any{"type": "ADDED", "object": object})
							_ = encoder.Encode(map[string]any{"type": "BOOKMARK", "object": map[string]any{"apiVersion": resource.version.String(), "kind": resource.kind, "metadata": map[string]any{"resourceVersion": "2", "annotations": map[string]string{"k8s.io/initial-events-end": "true"}}}})
						}
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": resource.version.String(), "kind": resource.kind + "List", "metadata": map[string]string{"resourceVersion": "1"}, "items": []any{object}})
				}))
				defer server.Close()
				mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{corev1.SchemeGroupVersion, farapi.GroupVersion})
				for _, registered := range resources {
					mapper.Add(registered.version.WithKind(registered.kind), meta.RESTScopeNamespace)
					mapper.Add(registered.version.WithKind(registered.kind+"List"), meta.RESTScopeNamespace)
				}
				options := safetyCacheOptions(farapi.SafetyConfig{Enabled: enabled, CredentialNamespace: "bmc"})
				options.Scheme = scheme
				options.Mapper = mapper
				actual, err := cache.New(&rest.Config{Host: server.URL}, options)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := actual.GetInformer(ctx, resource.object.DeepCopyObject().(client.Object)); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- actual.Start(ctx) }()
				if !actual.WaitForCacheSync(ctx) {
					t.Fatal("cache did not sync")
				}
				list := resource.list.DeepCopyObject().(client.ObjectList)
				if err := actual.List(ctx, list); err != nil {
					t.Fatal(err)
				}
				items, err := meta.ExtractList(list)
				if err != nil || len(items) != 1 {
					t.Fatalf("cache control missing: error=%v items=%d", err, len(items))
				}
				object := resource.object.DeepCopyObject().(client.Object)
				err = actual.Get(ctx, client.ObjectKey{Namespace: "foreign", Name: "fixture"}, object)
				if enabled && err == nil {
					t.Error("foreign namespace admitted by safety cache")
				}
				if !enabled && err != nil {
					t.Fatalf("default foreign-namespace control: %v", err)
				}
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				mutex.Lock()
				defer mutex.Unlock()
				if len(paths) == 0 {
					t.Fatal("cache issued no requests")
				}
				for _, path := range paths {
					if enabled && path != scopedPath {
						t.Errorf("safety cache requested %s, want %s", path, scopedPath)
					}
				}
				if !enabled && paths[0] != globalPath {
					t.Errorf("default cache changed: %v", paths)
				}
			})
		}
	}
}
