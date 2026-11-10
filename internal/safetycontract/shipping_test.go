package safetycontract

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

func readYAMLObjects(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var objects []map[string]any
	for {
		var object map[string]any
		if err := decoder.Decode(&object); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if len(object) > 0 {
			objects = append(objects, object)
		}
	}
	return objects
}

func checkCRDCopy(source, bundled map[string]any, template bool) error {
	normalized := (&unstructured.Unstructured{Object: bundled}).DeepCopy().Object
	if stamp, found, err := unstructured.NestedFieldNoCopy(normalized, "metadata", "creationTimestamp"); err != nil || (found && stamp != nil) {
		return fmt.Errorf("unexpected bundled creation timestamp")
	}
	unstructured.RemoveNestedField(normalized, "metadata", "creationTimestamp")
	if labels, found, err := unstructured.NestedStringMap(normalized, "metadata", "labels"); err != nil || (found && !reflect.DeepEqual(labels, map[string]string{"app.kubernetes.io/name": "fence-agents-remediation-operator"})) {
		return fmt.Errorf("unexpected bundled CRD labels")
	}
	unstructured.RemoveNestedField(normalized, "metadata", "labels")
	if status, found := normalized["status"]; found {
		if !reflect.DeepEqual(status, map[string]any{"acceptedNames": map[string]any{"kind": "", "plural": ""}, "conditions": nil, "storedVersions": nil}) {
			return fmt.Errorf("unexpected bundled CRD status")
		}
		delete(normalized, "status")
	}
	if !reflect.DeepEqual(source, normalized) {
		return fmt.Errorf("bundled CRD differs from generated runtime schema")
	}
	versions, found, err := unstructured.NestedSlice(source, "spec", "versions")
	if err != nil || !found {
		return fmt.Errorf("CRD versions missing")
	}
	for _, value := range versions {
		version, ok := value.(map[string]any)
		if !ok || version["name"] != "v1alpha1" {
			continue
		}
		path := []string{"schema", "openAPIV3Schema", "properties", "spec", "properties"}
		if template {
			path = append(path, "template", "properties", "spec", "properties")
		}
		path = append(path, "nodeBinding")
		binding, found, err := unstructured.NestedMap(version, path...)
		if err != nil || !found || binding["type"] != "object" {
			return fmt.Errorf("typed Node binding schema missing")
		}
		for _, field := range []string{"version", "name", "uid", "systemUUID"} {
			if _, found, err := unstructured.NestedMap(binding, "properties", field); err != nil || !found {
				return fmt.Errorf("Node binding property missing")
			}
		}
		return nil
	}
	return fmt.Errorf("v1alpha1 CRD schema missing")
}

func checkNodeDeletionRules(rules []any) error {
	contains := func(values any, want string) bool {
		items, ok := values.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if item == want || item == "*" {
				return true
			}
		}
		return false
	}
	for _, value := range rules {
		rule, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid RBAC rule")
		}
		if contains(rule["apiGroups"], "") && contains(rule["resources"], "nodes") && (contains(rule["verbs"], "delete") || contains(rule["verbs"], "deletecollection")) {
			return fmt.Errorf("shipping grant permits Node deletion")
		}
	}
	return nil
}

func TestShippingSchemasAndNodePermissions(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, template := range []bool{false, true} {
		plural := "fenceagentsremediations"
		if template {
			plural = "fenceagentsremediationtemplates"
		}
		name := "fence-agents-remediation.medik8s.io_" + plural + ".yaml"
		source := readYAMLObjects(t, filepath.Join(root, "config", "crd", "bases", name))[0]
		bundled := readYAMLObjects(t, filepath.Join(root, "bundle", "manifests", name))[0]
		if err := checkCRDCopy(source, bundled, template); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		mutated := (&unstructured.Unstructured{Object: bundled}).DeepCopy().Object
		if err := unstructured.SetNestedField(mutated, "Cluster", "spec", "scope"); err != nil {
			t.Fatal(err)
		}
		if err := checkCRDCopy(source, mutated, template); err == nil {
			t.Fatal("CRD parity check missed fixed-input mutation")
		}
	}
	var paths []string
	for _, pattern := range []string{filepath.Join(root, "config", "rbac", "*.yaml"), filepath.Join(root, "config", "rbac", "safety", "*.yaml"), filepath.Join(root, "bundle", "manifests", "*.clusterserviceversion.yaml"), filepath.Join(root, "config", "manifests", "bases", "*.clusterserviceversion.yaml")} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			t.Fatalf("no shipping RBAC/CSV objects matched %s", pattern)
		}
		paths = append(paths, matches...)
	}
	for _, path := range paths {
		for _, object := range readYAMLObjects(t, path) {
			if rules, found, err := unstructured.NestedSlice(object, "rules"); err != nil {
				t.Fatal(err)
			} else if found {
				if err := checkNodeDeletionRules(rules); err != nil {
					t.Errorf("%s: %v", path, err)
				}
			}
			for _, grant := range []string{"clusterPermissions", "permissions"} {
				permissions, _, err := unstructured.NestedSlice(object, "spec", "install", "spec", grant)
				if err != nil {
					t.Fatal(err)
				}
				for _, value := range permissions {
					permission, ok := value.(map[string]any)
					if !ok {
						t.Fatal("invalid CSV permission")
					}
					rules, _, err := unstructured.NestedSlice(permission, "rules")
					if err != nil {
						t.Fatal(err)
					}
					if err := checkNodeDeletionRules(rules); err != nil {
						t.Errorf("%s: %v", path, err)
					}
				}
			}
		}
	}
	for _, verb := range []string{"delete", "deletecollection", "*"} {
		if err := checkNodeDeletionRules([]any{map[string]any{"apiGroups": []any{""}, "resources": []any{"nodes"}, "verbs": []any{verb}}}); err == nil {
			t.Fatal("RBAC guard missed fixed-input deletion mutation")
		}
	}
}
