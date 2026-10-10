/*
Copyright 2021 KubeCube Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rbac

import (
	"os"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

// clusterScoped are the resources a tenant or a project must not be able to
// write, because a rule about them is a rule about the whole cluster: their
// bindings are ClusterRoleBindings, so there is no namespace to confine them
// to.
var clusterScoped = []string{
	"namespaces",
	"namespaces/status",
	"nodes",
	"nodes/status",
	"persistentvolumes",
	"persistentvolumes/status",
	"storageclasses",
	"customresourcedefinitions",
}

var writeVerbs = []string{"create", "update", "patch", "delete", "deletecollection"}

// TestTheBuiltInRolesKeepClusterScopedWritesOut reads the roles the chart
// actually ships.
//
// The chart is a sibling repository, so CI — which checks out this one alone —
// skips this test. It is what a person runs after changing a built-in role:
//
//	helm template kubecube ../kubecube-chart \
//	  --set-string global.componentsEnable.kubecube=true > rendered.yaml
//	KUBECUBE_RENDERED_CHART=rendered.yaml go test ./pkg/authorizer/rbac/
//
// Reading the rendered output rather than the templates is the point: the
// aggregation labels, the templates' conditionals and the subcharts all decide
// what a role ends up holding, and only the rendered rules show that.
func TestTheBuiltInRolesKeepClusterScopedWritesOut(t *testing.T) {
	path := os.Getenv("KUBECUBE_RENDERED_CHART")
	if path == "" {
		t.Skip("set KUBECUBE_RENDERED_CHART to the chart rendered by helm template")
	}

	roles := renderedClusterRoles(t, path)

	// The roles a tenant or a project administrator ends up holding, and the one
	// an ordinary user holds.
	for _, name := range []string{
		"aggregate-to-tenant-admin-cluster",
		"aggregate-to-project-admin-cluster",
		"aggregate-to-reviewer",
		"reviewer",
	} {
		role, ok := roles[name]
		if !ok {
			t.Errorf("the chart no longer renders %s, and this check is about what it holds", name)
			continue
		}

		for _, rule := range role.Rules {
			for _, resource := range rule.Resources {
				if !isClusterScoped(resource) {
					continue
				}
				for _, verb := range rule.Verbs {
					if !isWrite(verb) {
						continue
					}
					t.Errorf("%s may %s %s, which is the whole cluster and not a tenant's own",
						name, verb, resource)
				}
			}
		}
	}

	// And the other direction: the platform administrator must still be able to
	// do what the platform does, or this check would pass by locking everybody
	// out.
	platformAdmin, ok := roles["aggregate-to-platform-admin"]
	if !ok {
		t.Fatal("the chart no longer renders aggregate-to-platform-admin")
	}
	if !holds(platformAdmin, "namespaces", "update") {
		t.Error("the platform administrator can no longer update a namespace, and the platform creates and owns them")
	}
}

func isClusterScoped(resource string) bool {
	for _, known := range clusterScoped {
		if resource == known {
			return true
		}
	}
	return false
}

func isWrite(verb string) bool {
	for _, known := range writeVerbs {
		if verb == known {
			return true
		}
	}
	return false
}

func holds(role *rbacv1.ClusterRole, resource, verb string) bool {
	for _, rule := range role.Rules {
		for _, held := range rule.Resources {
			if held != resource {
				continue
			}
			for _, allowed := range rule.Verbs {
				if allowed == verb || allowed == "*" {
					return true
				}
			}
		}
	}
	return false
}

// renderedClusterRoles reads a rendered chart and keeps the ClusterRoles in it.
//
// The documents are split by hand because a rendered chart is a stream of them
// and sigs.k8s.io/yaml reads one document at a time.
func renderedClusterRoles(t *testing.T, path string) map[string]*rbacv1.ClusterRole {
	t.Helper()

	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	roles := map[string]*rbacv1.ClusterRole{}
	for _, part := range strings.Split(string(document), "\n---") {
		if !strings.Contains(part, "kind: ClusterRole") {
			continue
		}

		role := &rbacv1.ClusterRole{}
		if err := yaml.Unmarshal([]byte(part), role); err != nil {
			t.Fatalf("reading a rendered ClusterRole: %v", err)
		}
		if role.Name == "" {
			continue
		}
		roles[role.Name] = role
	}

	if len(roles) == 0 {
		t.Fatalf("%s holds no ClusterRole, so nothing was checked", path)
	}

	return roles
}

// TestThePlatformRunsAsItsOwnServiceAccount reads the same rendered chart and
// asks the questions the platform's own identity has to answer: that it is a
// ServiceAccount of its own rather than the namespace's `default`, that the
// Deployments actually name it, and that no role anywhere in the chart is
// `*/*/*`.
//
// The last one is the reason this check exists at all: an unbounded role cannot
// be reviewed, and the platform used to ship one bound to `default` — the
// identity every pod in the namespace gets by not asking for one.
func TestThePlatformRunsAsItsOwnServiceAccount(t *testing.T) {
	path := os.Getenv("KUBECUBE_RENDERED_CHART")
	if path == "" {
		t.Skip("set KUBECUBE_RENDERED_CHART to the chart rendered by helm template")
	}

	documents := renderedDocuments(t, path)

	// 1. The ServiceAccount exists, and it is not `default`.
	serviceAccounts := namesOf(documents, "ServiceAccount")
	if !serviceAccounts["kubecube"] {
		t.Errorf("the chart does not render a kubecube ServiceAccount, and the platform is supposed to run as one")
	}

	// 2. The binding that carries the platform's permissions names it.
	binding, ok := documentNamed(documents, "ClusterRoleBinding", "kubecube-rolebinding")
	if !ok {
		t.Fatal("the chart no longer renders kubecube-rolebinding")
	}
	subjects, _ := binding["subjects"].([]interface{})
	if len(subjects) == 0 {
		t.Fatal("kubecube-rolebinding binds to nobody")
	}
	for _, subject := range subjects {
		entry, _ := subject.(map[string]interface{})
		if name, _ := entry["name"].(string); name != "kubecube" {
			t.Errorf("kubecube-rolebinding binds to %q, want the platform's own ServiceAccount", name)
		}
	}

	// 3. The Deployments name it, so the identity is the one they run as.
	named := 0
	for _, document := range documents {
		if kind, _ := document["kind"].(string); kind != "Deployment" {
			continue
		}
		metadata, _ := document["metadata"].(map[string]interface{})
		name, _ := metadata["name"].(string)
		if name != "kubecube" && name != "warden" {
			continue
		}
		if serviceAccountOf(document) != "kubecube" {
			t.Errorf("Deployment %s runs as %q, want the platform's own ServiceAccount",
				name, serviceAccountOf(document))
			continue
		}
		named++
	}
	if named != 2 {
		t.Errorf("%d of the platform's Deployments name the ServiceAccount, want 2", named)
	}

	// 4. No role in the chart is everything.
	for _, document := range documents {
		if kind, _ := document["kind"].(string); kind != "ClusterRole" {
			continue
		}
		metadata, _ := document["metadata"].(map[string]interface{})
		name, _ := metadata["name"].(string)

		rules, _ := document["rules"].([]interface{})
		for _, rule := range rules {
			entry, _ := rule.(map[string]interface{})
			if coversEverything(entry) {
				t.Errorf("ClusterRole %s holds a rule over every resource with every verb", name)
			}
		}
	}
}

func coversEverything(rule map[string]interface{}) bool {
	return listHolds(rule["apiGroups"], "*") && listHolds(rule["resources"], "*") && listHolds(rule["verbs"], "*")
}

func listHolds(value interface{}, wanted string) bool {
	items, _ := value.([]interface{})
	for _, item := range items {
		if text, _ := item.(string); text == wanted {
			return true
		}
	}
	return false
}

func serviceAccountOf(deployment map[string]interface{}) string {
	spec, _ := deployment["spec"].(map[string]interface{})
	template, _ := spec["template"].(map[string]interface{})
	podSpec, _ := template["spec"].(map[string]interface{})
	name, _ := podSpec["serviceAccountName"].(string)
	return name
}

func namesOf(documents []map[string]interface{}, kind string) map[string]bool {
	names := map[string]bool{}
	for _, document := range documents {
		if documentKind, _ := document["kind"].(string); documentKind != kind {
			continue
		}
		metadata, _ := document["metadata"].(map[string]interface{})
		if name, _ := metadata["name"].(string); name != "" {
			names[name] = true
		}
	}
	return names
}

func documentNamed(documents []map[string]interface{}, kind, name string) (map[string]interface{}, bool) {
	for _, document := range documents {
		if documentKind, _ := document["kind"].(string); documentKind != kind {
			continue
		}
		metadata, _ := document["metadata"].(map[string]interface{})
		if documentName, _ := metadata["name"].(string); documentName == name {
			return document, true
		}
	}
	return nil, false
}

// renderedDocuments reads a rendered chart into one map per document.
func renderedDocuments(t *testing.T, path string) []map[string]interface{} {
	t.Helper()

	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	documents := make([]map[string]interface{}, 0, 64)
	for _, part := range strings.Split(string(document), "\n---") {
		if strings.TrimSpace(part) == "" {
			continue
		}

		parsed := map[string]interface{}{}
		if err := yaml.Unmarshal([]byte(part), &parsed); err != nil {
			// A rendered chart holds the CRDs too, and a document this reader
			// cannot parse is one it has no questions about.
			continue
		}
		if len(parsed) > 0 {
			documents = append(documents, parsed)
		}
	}

	if len(documents) == 0 {
		t.Fatalf("%s holds no document, so nothing was checked", path)
	}

	return documents
}
