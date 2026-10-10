/*
Copyright 2023 KubeCube Authors

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
	"fmt"
	"sort"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	Null  VerbRepresent = "null"
	Read  VerbRepresent = "read"
	Write VerbRepresent = "write"
	All   VerbRepresent = "all"
)

type VerbRepresent string

// known reports whether a verb is one a request may ask for. A client sends
// this value, so anything outside the set is a request to be refused rather
// than a verb to write into a rule.
func (v VerbRepresent) known() bool {
	switch v {
	case Read, Write, All:
		return true
	default:
		return false
	}
}

var (
	readVerbs        = sets.NewString("get", "list", "watch")
	writeVerbs       = sets.NewString("create", "delete", "patch", "update", "deletecollection")
	legacyWriteVerbs = sets.NewString("create", "delete", "patch", "update")
	bothVerbs        = readVerbs.Union(writeVerbs)
)

// RoleAuthBody the another transformed form of ClusterRole.
type RoleAuthBody struct {
	ClusterRoleName string              `json:"clusterRoleName,omitempty"`
	Scope           string              `json:"scope"`
	AuthItems       map[string]AuthItem `json:"authItems"`
}

// RoleItemsBody is what a console sends when it saves a role: the items it
// chose, at the level it is choosing them at.
//
// It carries no rules. Rules are derived on the server from the catalogue, so
// what a caller sends is a selection from a list the deployment defines rather
// than a rule set the caller invented.
type RoleItemsBody struct {
	ClusterRoleName string                   `json:"clusterRoleName"`
	Scope           string                   `json:"scope"`
	Items           map[string]VerbRepresent `json:"items"`
}

type AuthItem struct {
	Verb      VerbRepresent            `json:"verb"`
	Resources map[string]VerbRepresent `json:"resources,omitempty"`
}

// ClusterRoleSplit holds the result of ClusterRole.
type ClusterRoleSplit map[string]VerbRepresent

// SplitClusterRole split ClusterRole as into format:
// deployments: All
// services: Read
// clusters: Write
func SplitClusterRole(clusterRole *rbacv1.ClusterRole) ClusterRoleSplit {
	res := make(map[string]VerbRepresent)
	if clusterRole == nil {
		return res
	}
	for _, rule := range clusterRole.Rules {
		verb := verbsAssert(rule.Verbs)
		for _, resource := range rule.Resources {
			if verbRepresent, ok := res[resource]; ok {
				res[resource] = verbsMerge(verbRepresent, verb)
			} else {
				res[resource] = verb
			}
		}
	}
	return res
}

func verbsMerge(v1, v2 VerbRepresent) VerbRepresent {
	if v1 == v2 {
		return v1
	}
	if (v1 != v2) && (v1 != Null) && (v2 != Null) {
		return All
	}
	return Null
}

// verbsAssert asserts verbs as VerbRepresent.
func verbsAssert(verbs []string) VerbRepresent {
	currentVerbs := sets.NewString(verbs...)
	switch {
	case bothVerbs.Equal(currentVerbs):
		return All
	case currentVerbs.IsSuperset(readVerbs):
		return Read
	case currentVerbs.IsSuperset(writeVerbs):
		return Write
	case currentVerbs.IsSuperset(legacyWriteVerbs):
		return Write
	}
	return Null
}

// ClusterRoleMapping mappings ClusterRole as RoleAuthBody by configmap data.
// cmData format as:
// deployments: "deployments;pods;replicasets;pods/status;deployments/status"
// services: "services;endpoints;pods"
func ClusterRoleMapping(clusterRole *rbacv1.ClusterRole, cmData map[string]string, verbose bool) *RoleAuthBody {
	if len(cmData) == 0 || cmData == nil || clusterRole == nil {
		return nil
	}

	processedClusterRole := SplitClusterRole(clusterRole)

	res := &RoleAuthBody{ClusterRoleName: clusterRole.Name, AuthItems: make(map[string]AuthItem)}
	for k, v := range cmData {
		var (
			visitVerb     VerbRepresent
			interruptVerb VerbRepresent
			placeVerb     VerbRepresent
		)
		authItem := AuthItem{Resources: map[string]VerbRepresent{}}
		resources := strings.Split(v, ";")
		// k: deployment.manager
		// v: deployments;services;pods;pods/logs
		for _, resource := range resources {
			verb, hasRule := processedClusterRole[resource]
			if verb == "" {
				verb = Null
			}
			authItem.Resources[resource] = verb

			// if ClusterRole had no this auth item, early set interruptVerb Null.
			if !hasRule || verb == Null {
				interruptVerb = Null
				continue
			}

			// interruptVerb will be Null if meet those condition:
			// 1. Write != Read
			// 3. Null != Read
			// 3. Null != Write
			if (verb != All) && (visitVerb != All) && (verb != visitVerb) && (visitVerb != "") {
				interruptVerb = Null
				continue
			}

			// placeVerb will be Null, Write, Read
			if placeVerb == "" || placeVerb == All {
				placeVerb = verb
			}

			// to visit next resources and verb
			visitVerb = verb
		}

		switch {
		case interruptVerb == Null:
			authItem.Verb = Null
		case visitVerb != placeVerb:
			authItem.Verb = placeVerb
		default:
			authItem.Verb = visitVerb
		}

		if !verbose {
			authItem.Resources = nil
		}

		res.AuthItems[k] = authItem
	}
	return res
}

// RoleAuthMapping mapping RoleAuthBody as ClusterRole by configmap data.
//
// It is the only place a role's rules are generated, and it refuses rather than
// skips: an item the view it was given does not hold is an error, because a role
// that quietly lost an item is a permission nobody can explain afterwards.
func RoleAuthMapping(roleAuths *RoleAuthBody, cmData map[string]string) (*rbacv1.ClusterRole, error) {
	if roleAuths == nil {
		return nil, fmt.Errorf("no role was given")
	}
	if len(cmData) == 0 {
		return nil, fmt.Errorf("no catalogue view was given, so no item could be resolved")
	}

	rules := make(map[string]VerbRepresent)

	for k, v := range roleAuths.AuthItems {
		if v.Verb == Null {
			continue
		}

		resources, ok := cmData[k]
		if !ok {
			return nil, fmt.Errorf("the catalogue does not hold %q at this level, so the role cannot be built from it", k)
		}

		for _, resource := range strings.Split(resources, ";") {
			if resource = strings.TrimSpace(resource); resource == "" {
				continue
			}
			verb, ok := rules[resource]
			if !ok {
				rules[resource] = v.Verb
				continue
			}
			if verb != v.Verb {
				rules[resource] = All
			}
		}
	}

	// Sorted, so the same request always writes the same role: a rule list that
	// came out of a map would otherwise reorder itself on every save.
	resources := make([]string, 0, len(rules))
	for resource := range rules {
		resources = append(resources, resource)
	}
	sort.Strings(resources)

	policyRules := make([]rbacv1.PolicyRule, 0, len(resources))

	for _, resource := range resources {
		var verbs []string
		switch rules[resource] {
		case All:
			verbs = bothVerbs.List()
		case Read:
			verbs = readVerbs.List()
		case Write:
			verbs = writeVerbs.List()
		default:
			continue
		}
		sort.Strings(verbs)

		policyRules = append(policyRules, rbacv1.PolicyRule{
			APIGroups: []string{"*"},
			Resources: []string{resource},
			Verbs:     verbs,
		})
	}

	return &rbacv1.ClusterRole{ObjectMeta: v1.ObjectMeta{Name: roleAuths.ClusterRoleName}, Rules: policyRules}, nil
}

// DeriveRole answers what one request for a role means: every item is validated
// against the catalogue at the level the request named, and the rules come from
// RoleAuthMapping.
//
// Validation happens here and generation happens there, so a request can be
// refused with the reason it earned — an item nobody defined, an item the level
// does not allow, a verb that is not a verb — while there is still exactly one
// place that writes a rule.
func DeriveRole(catalogue *Catalogue, level Level, name string, items map[string]VerbRepresent) (*rbacv1.ClusterRole, error) {
	if catalogue == nil {
		return nil, fmt.Errorf("no catalogue was given, so nothing could be granted")
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("the role has no name")
	}

	asked := make([]string, 0, len(items))
	for item := range items {
		asked = append(asked, item)
	}
	sort.Strings(asked)

	authItems := make(map[string]AuthItem, len(items))
	for _, item := range asked {
		verb := items[item]
		// A cleared box is not a permission; Null and the empty string are how a
		// console says "not granted".
		if verb == Null || verb == "" {
			continue
		}
		if !verb.known() {
			return nil, fmt.Errorf("item %s asks for the verb %q, which is not one of %s, %s or %s",
				item, verb, Read, Write, All)
		}
		if _, err := catalogue.Grant(item, level); err != nil {
			return nil, err
		}
		authItems[item] = AuthItem{Verb: verb}
	}

	return RoleAuthMapping(&RoleAuthBody{ClusterRoleName: name, AuthItems: authItems}, catalogue.FlatItemsAt(level))
}
