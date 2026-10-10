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

package mapping

import (
	"reflect"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
)

func testCatalogue(t *testing.T) *Catalogue {
	t.Helper()

	catalogue, err := ParseCatalogue(map[string]string{CatalogueKey: catalogueDocument})
	if err != nil {
		t.Fatalf("ParseCatalogue: %v", err)
	}
	return catalogue
}

func TestLevelOf(t *testing.T) {
	for _, scope := range []string{"platform", "tenant", "project"} {
		level, err := LevelOf(scope)
		if err != nil {
			t.Fatalf("LevelOf(%q): %v", scope, err)
		}
		if string(level) != scope {
			t.Errorf("level = %q, want %q", level, scope)
		}
	}

	for _, scope := range []string{"", "cluster", "Platform"} {
		if level, err := LevelOf(scope); err == nil {
			t.Errorf("LevelOf(%q) = %q, want a refusal", scope, level)
		}
	}
}

func TestDeriveRoleGeneratesRulesFromItems(t *testing.T) {
	catalogue := testCatalogue(t)

	role, err := DeriveRole(catalogue, LevelProject, "shop-web-editor", map[string]VerbRepresent{
		"deployments.manage": Read,
	})
	if err != nil {
		t.Fatalf("DeriveRole: %v", err)
	}

	if role.Name != "shop-web-editor" {
		t.Errorf("name = %q, want the role the request named", role.Name)
	}
	if len(role.Rules) == 0 {
		t.Fatal("the role has no rules")
	}

	byResource := make(map[string][]string, len(role.Rules))
	for _, rule := range role.Rules {
		if len(rule.Resources) != 1 {
			t.Errorf("rule covers %v, want one resource per rule", rule.Resources)
		}
		byResource[rule.Resources[0]] = rule.Verbs
	}

	deployments, ok := byResource["deployments"]
	if !ok {
		t.Fatalf("rules = %v, want the item's resources", byResource)
	}
	for _, verb := range []string{"get", "list", "watch"} {
		if !contains(deployments, verb) {
			t.Errorf("deployments verbs = %v, want %s among them", deployments, verb)
		}
	}
	if contains(deployments, "delete") {
		t.Errorf("deployments verbs = %v, want no write verb for a read item", deployments)
	}
}

// The three refusals a save can earn, each naming what was wrong: an item nobody
// defined, an item the level does not allow, and a verb that is not a verb.
func TestDeriveRoleRefuses(t *testing.T) {
	catalogue := testCatalogue(t)

	tests := []struct {
		name      string
		catalogue *Catalogue
		role      string
		level     Level
		items     map[string]VerbRepresent
		reasonHas string
	}{
		{
			name: "an item the catalogue does not hold", catalogue: catalogue,
			role: "r", level: LevelProject, items: map[string]VerbRepresent{"secrets.manage": Read},
			reasonHas: "does not hold",
		},
		{
			name: "an item at a level that does not allow it", catalogue: catalogue,
			role: "r", level: LevelTenant, items: map[string]VerbRepresent{"crds.manage": Read},
			reasonHas: "not grantable at the tenant level",
		},
		{
			name: "a verb that is not a verb", catalogue: catalogue,
			role: "r", level: LevelProject, items: map[string]VerbRepresent{"deployments.manage": VerbRepresent("admin")},
			reasonHas: "not one of read, write or all",
		},
		{
			name: "no catalogue at all", catalogue: nil,
			role: "r", level: LevelProject, items: map[string]VerbRepresent{"deployments.manage": Read},
			reasonHas: "no catalogue",
		},
		{
			name: "a role with no name", catalogue: catalogue,
			role: "", level: LevelProject, items: map[string]VerbRepresent{"deployments.manage": Read},
			reasonHas: "no name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, err := DeriveRole(tt.catalogue, tt.level, tt.role, tt.items)
			if err == nil {
				t.Fatalf("DeriveRole = %+v, want a refusal", role)
			}
			if !strings.Contains(err.Error(), tt.reasonHas) {
				t.Errorf("error = %q, want it to carry %q", err.Error(), tt.reasonHas)
			}
		})
	}
}

// A cleared box is not a permission: the item is left out rather than refused.
func TestDeriveRoleLeavesClearedItemsOut(t *testing.T) {
	catalogue := testCatalogue(t)

	role, err := DeriveRole(catalogue, LevelProject, "r", map[string]VerbRepresent{
		"deployments.manage": Null,
		"pods.manage":        "",
	})
	if err != nil {
		t.Fatalf("DeriveRole: %v", err)
	}
	if len(role.Rules) != 0 {
		t.Errorf("rules = %v, want none for cleared items", role.Rules)
	}
}

// The same request writes the same role, rule for rule: a list that came out of
// a map would otherwise reorder itself on every save, and every save would look
// like a change.
func TestDeriveRoleIsDeterministic(t *testing.T) {
	catalogue, err := ParseCatalogue(map[string]string{CatalogueKey: `
items:
  deployments.manage:
    resources: "deployments;replicasets"
    levels: [platform, tenant, project]
  pods.manage:
    resources: "pods"
    levels: [platform, tenant, project]
  services.manage:
    resources: "services;endpoints"
    levels: [platform, tenant, project]
  configmaps.manage:
    resources: "configmaps"
    levels: [platform, tenant, project]
`})
	if err != nil {
		t.Fatalf("ParseCatalogue: %v", err)
	}

	items := map[string]VerbRepresent{
		"deployments.manage": All,
		"pods.manage":        Read,
		"services.manage":    Write,
		"configmaps.manage":  Read,
	}

	first, err := DeriveRole(catalogue, LevelProject, "r", items)
	if err != nil {
		t.Fatalf("DeriveRole: %v", err)
	}
	second, err := DeriveRole(catalogue, LevelProject, "r", items)
	if err != nil {
		t.Fatalf("DeriveRole: %v", err)
	}

	if !reflect.DeepEqual(first.Rules, second.Rules) {
		t.Errorf("rules differ between two identical requests:\n%v\n%v", first.Rules, second.Rules)
	}

	resources := make([]string, 0, len(first.Rules))
	for _, rule := range first.Rules {
		resources = append(resources, rule.Resources[0])
	}
	for i := 1; i < len(resources); i++ {
		if resources[i-1] > resources[i] {
			t.Fatalf("resources = %v, want them sorted", resources)
		}
	}
}

// Every item at every verb survives the round trip through rules and back, one
// item at a time. Items that share a resource merge — that is what the merge is
// for — so the property is stated for one item, which is what a person ticks.
func TestTheRoundTripOfOneItem(t *testing.T) {
	catalogue := testCatalogue(t)

	for _, level := range []Level{LevelPlatform, LevelTenant, LevelProject} {
		for _, name := range catalogue.Names() {
			if _, err := catalogue.Grant(name, level); err != nil {
				continue
			}

			for _, verb := range []VerbRepresent{Read, Write, All} {
				role, err := DeriveRole(catalogue, level, "r", map[string]VerbRepresent{name: verb})
				if err != nil {
					t.Fatalf("DeriveRole(%s, %s): %v", name, verb, err)
				}

				back := ClusterRoleMapping(role, catalogue.FlatItemsAt(level), false)
				if back == nil {
					t.Fatalf("ClusterRoleMapping returned nothing for %s", name)
				}

				item, ok := back.AuthItems[name]
				if !ok {
					t.Fatalf("%s is missing from the role it was just granted in", name)
				}
				if item.Verb != verb {
					t.Errorf("%s at %s: %s came back as %s", name, level, verb, item.Verb)
				}
			}
		}
	}
}

// The rules a role is built from carry the API group the catalogue implies. A
// rule with no resources would mean every resource, so that is asserted too.
func TestDerivedRulesAreWellFormed(t *testing.T) {
	catalogue := testCatalogue(t)

	role, err := DeriveRole(catalogue, LevelPlatform, "r", map[string]VerbRepresent{"crds.manage": Write})
	if err != nil {
		t.Fatalf("DeriveRole: %v", err)
	}

	for _, rule := range role.Rules {
		if len(rule.Resources) == 0 {
			t.Error("a rule with no resources means every resource")
		}
		if len(rule.Verbs) == 0 {
			t.Error("a rule with no verbs means nothing, which is not what was asked for")
		}
		if len(rule.APIGroups) == 0 {
			t.Error("a rule with no API groups means the core group only")
		}
	}

	if len(role.Rules) != 1 || role.Rules[0].Resources[0] != "customresourcedefinitions" {
		t.Errorf("rules = %v, want the item's own resource", role.Rules)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

var _ = rbacv1.ClusterRole{}
