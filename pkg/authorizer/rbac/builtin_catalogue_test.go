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
	"sort"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecube-io/kubecube/pkg/authorizer/mapping"
)

// builtinAggregatingRoles are the six ClusterRoles the chart ships for the
// built-in levels. They carry no rules of their own — the chart writes
// `rules: []` and an aggregationRule — so what each one actually holds is
// whatever Kubernetes fills it with, which this test resolves by hand.
var builtinAggregatingRoles = []string{
	"platform-admin",
	"tenant-admin",
	"project-admin",
	"reviewer",
	"tenant-admin-cluster",
	"project-admin-cluster",
}

// itemsNothingCanHoldYet would name the catalogue items no built-in role holds.
//
// It is empty now. It used to carry resourcegroups.manage and resourcegroups.scope,
// which the catalogue declared ahead of the feature that would evaluate them: the
// chart installed no app.kubecube.io CRD, no built-in role granted resourcegroups,
// resourcegroups/status or resourcegroups/members, and the string
// `resourcegroups/members` was named nowhere else in the repository. The decision
// taken was to remove the two items rather than keep offering a checkbox that can
// never be ticked, so the catalogue no longer holds them and the assertion below
// now covers every item it does hold.
//
// Keep the list. An item that no role can hold should be recorded here with the
// same reasoning, and reported on every run, rather than added to the catalogue
// and left silently unchecked.
var itemsNothingCanHoldYet = map[string]bool{}

// TestTheBuiltInRolesHoldTheCatalogue reads the catalogue the chart ships and
// the built-in roles it ships with it, and asks the two questions that keep the
// server's authority and the roles it installs from drifting apart:
//
//   - every item the catalogue offers is fully held by at least one built-in
//     role, or is named in itemsNothingCanHoldYet with the decision it waits on;
//   - every resource a built-in role grants that the catalogue has no item for
//     is reported, so a grant nobody can hand out or review is visible.
//
// "Fully held" is mapping.ClusterRoleMapping's answer, not a weaker one this
// test invents. It is the same function the server uses to decide what a role's
// rules amount to, and one resource the role does not hold makes the whole item
// Null — so a role that holds every resource of an item except one does not
// half-hold it, it does not hold it.
//
// The chart is a sibling repository, so CI — which checks out this one alone —
// has no copy of it and this test skips there. It is what a person runs after
// changing the catalogue or a built-in role:
//
//	helm template kubecube ../kubecube-chart \
//	  --set-string global.componentsEnable.kubecube=true > rendered.yaml
//	KUBECUBE_RENDERED_CHART=rendered.yaml go test ./pkg/authorizer/rbac/
//
// Reading the rendered output rather than the templates is the point here too:
// the aggregation labels, the templates' conditionals and the subcharts all
// decide what a role ends up holding, and only the rendered rules and labels
// show that.
func TestTheBuiltInRolesHoldTheCatalogue(t *testing.T) {
	path := os.Getenv("KUBECUBE_RENDERED_CHART")
	if path == "" {
		// An explicit skip, not a silent pass: a green run without this
		// variable means the check did not run, and the message says so.
		t.Skip("set KUBECUBE_RENDERED_CHART to the chart rendered by helm template; without it this check does not run")
	}

	roles := renderedClusterRoles(t, path)
	catalogue := renderedCatalogue(t, path)

	// The catalogue's own view of itself, flattened over every level, so the
	// check covers every item rather than the ones one level happens to hold.
	flat := catalogue.FlatItemsAt(mapping.LevelPlatform, mapping.LevelTenant, mapping.LevelProject)

	holding := map[string][]string{}
	splits := map[string]mapping.ClusterRoleSplit{}
	for _, name := range builtinAggregatingRoles {
		aggregated := aggregatedClusterRole(t, roles, name)
		splits[name] = mapping.SplitClusterRole(aggregated)

		for item, authItem := range mapping.ClusterRoleMapping(aggregated, flat, true).AuthItems {
			if authItem.Verb != mapping.Null {
				holding[item] = append(holding[item], name)
			}
		}
	}

	// Every item the catalogue offers must be fully held, or be recorded above
	// with the reason nothing can hold it yet.
	for _, item := range catalogue.Names() {
		if len(holding[item]) > 0 {
			continue
		}
		if itemsNothingCanHoldYet[item] {
			t.Logf("GAP: item %s is held by no built-in role and is recorded as waiting on its implementation: %s",
				item, resourcesNotGranted(item, catalogue, splits))
			continue
		}
		t.Errorf("item %s is held by no built-in role: %s", item, resourcesNotGranted(item, catalogue, splits))
	}

	reportResourcesOutsideTheCatalogue(t, catalogue, splits)
}

// resourcesNotGranted explains why an item is not fully held: per resource the
// item names, which built-in roles hold it and as which verb. An item is Null
// either because a resource is held by nobody or because the roles that hold it
// disagree about the verb, and this is what tells the two apart.
func resourcesNotGranted(item string, catalogue *mapping.Catalogue, splits map[string]mapping.ClusterRoleSplit) string {
	reasons := make([]string, 0, len(catalogue.Items[item].ResourceNames()))
	for _, resource := range catalogue.Items[item].ResourceNames() {
		var held []string
		for name, split := range splits {
			if verb, ok := split[resource]; ok && verb != mapping.Null {
				held = append(held, name+"="+string(verb))
			}
		}
		if len(held) == 0 {
			reasons = append(reasons, resource+" is not held by any built-in role")
			continue
		}
		sort.Strings(held)
		reasons = append(reasons, resource+" is held as "+strings.Join(held, ", "))
	}
	sort.Strings(reasons)
	return strings.Join(reasons, "; ")
}

// reportResourcesOutsideTheCatalogue reports every resource a built-in role
// grants that no catalogue item names.
//
// It reports rather than asserts, and that is a measured fact about the chart
// rather than caution: the built-in aggregating roles are Kubernetes roles that
// also carry the cluster's own plumbing and the CRDs of every component the
// platform deploys — namespaces, nodes, storageclasses, clusterroles, leases,
// cilium and Calico types, OLM types, the monitoring types — while the
// catalogue only describes what a console may hand out. On the chart this test
// was written against that is 107 resources, so an assertion here would either
// fail forever or need a 107-entry exclusion list, and a list that long is the
// silent weakening this file is written to avoid. It is printed on every run so
// the set cannot drift unnoticed: a role that grows a resource is visible in
// the output even though it is not a failure.
func reportResourcesOutsideTheCatalogue(t *testing.T, catalogue *mapping.Catalogue, splits map[string]mapping.ClusterRoleSplit) {
	t.Helper()

	known := map[string]bool{}
	for _, item := range catalogue.Names() {
		for _, resource := range catalogue.Items[item].ResourceNames() {
			known[resource] = true
		}
	}

	unknown := map[string][]string{}
	for name, split := range splits {
		for resource := range split {
			if known[resource] {
				continue
			}
			unknown[resource] = append(unknown[resource], name)
		}
	}

	reported := make([]string, 0, len(unknown))
	for resource, holders := range unknown {
		sort.Strings(holders)
		reported = append(reported, resource+" ("+strings.Join(holders, ",")+")")
	}
	sort.Strings(reported)

	t.Logf("the catalogue has no item for %d resource(s) a built-in role grants: %s",
		len(reported), strings.Join(reported, " "))
}

// aggregatedClusterRole resolves what one built-in role runs with.
//
// An aggregating ClusterRole carries no rules of its own, because Kubernetes
// fills it from every ClusterRole its selectors match. A test that read only
// the role's own rules would see an empty role and "prove" that nothing is
// held, so the union is computed here the way the API server computes it: every
// role matching a selector contributes its rules, and a selector that matches
// nothing is a finding rather than an empty role.
func aggregatedClusterRole(t *testing.T, roles map[string]*rbacv1.ClusterRole, name string) *rbacv1.ClusterRole {
	t.Helper()

	role, ok := roles[name]
	if !ok {
		t.Fatalf("the chart no longer renders %s, and this check is about what it holds", name)
	}
	if role.AggregationRule == nil {
		return role
	}

	aggregated := &rbacv1.ClusterRole{ObjectMeta: role.ObjectMeta}
	aggregated.Rules = append(aggregated.Rules, role.Rules...)

	for _, declared := range role.AggregationRule.ClusterRoleSelectors {
		selector, err := metav1.LabelSelectorAsSelector(&declared)
		if err != nil {
			t.Fatalf("%s carries an aggregation selector this check cannot read: %v", name, err)
		}

		matched := 0
		for _, candidate := range roles {
			if candidate.Name == role.Name || !selector.Matches(labels.Set(candidate.Labels)) {
				continue
			}
			matched++
			aggregated.Rules = append(aggregated.Rules, candidate.Rules...)
		}
		if matched == 0 {
			t.Errorf("no rendered ClusterRole carries the label %v selects, so %s aggregates to nothing", declared, name)
		}
	}

	return aggregated
}

// renderedCatalogue reads the catalogue out of the ConfigMap the chart renders,
// so this check is about the items the deployment actually defines rather than
// a fixture that resembles them.
func renderedCatalogue(t *testing.T, path string) *mapping.Catalogue {
	t.Helper()

	configMap, ok := documentNamed(renderedDocuments(t, path), "ConfigMap", "kubecube-auth-mapping")
	if !ok {
		t.Fatal("the chart no longer renders the kubecube-auth-mapping ConfigMap, and this check is about the catalogue it holds")
	}

	data, _ := configMap["data"].(map[string]interface{})
	document, _ := data[mapping.CatalogueKey].(string)
	if strings.TrimSpace(document) == "" {
		t.Fatalf("the rendered ConfigMap carries no %s, so there is no catalogue to check", mapping.CatalogueKey)
	}

	catalogue, err := mapping.ParseCatalogue(map[string]string{mapping.CatalogueKey: document})
	if err != nil {
		t.Fatalf("the deployed catalogue does not parse: %v", err)
	}
	return catalogue
}
