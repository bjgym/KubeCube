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
	"strings"
	"testing"
)

// The shape the chart ships, with the two items that differ in level.
const catalogueDocument = `
items:
  deployments.manage:
    resources: "deployments;deployments/rollback;replicasets"
    levels: [platform, tenant, project]
  crds.manage:
    resources: "customresourcedefinitions"
    levels: [platform]
`

func TestParseCatalogue(t *testing.T) {
	catalogue, err := ParseCatalogue(map[string]string{CatalogueKey: catalogueDocument})
	if err != nil {
		t.Fatalf("ParseCatalogue: %v", err)
	}

	names := catalogue.Names()
	if len(names) != 2 || names[0] != "crds.manage" || names[1] != "deployments.manage" {
		t.Errorf("names = %v, want the items sorted", names)
	}

	deployments, ok := catalogue.Items["deployments.manage"]
	if !ok {
		t.Fatal("deployments.manage is missing")
	}
	resources := deployments.ResourceNames()
	if len(resources) != 3 || resources[1] != "deployments/rollback" {
		t.Errorf("resources = %v, want the semicolon-separated names", resources)
	}
	if len(deployments.Levels) != 3 || deployments.Levels[0] != LevelPlatform {
		t.Errorf("levels = %v, want the three levels", deployments.Levels)
	}
}

// The two refusals a request can earn: an item nobody defined, and an item at a
// level that does not allow it. Both have to be refusals rather than smaller
// answers, or a caller writes a rule the catalogue never described.
func TestGrant(t *testing.T) {
	catalogue, err := ParseCatalogue(map[string]string{CatalogueKey: catalogueDocument})
	if err != nil {
		t.Fatalf("ParseCatalogue: %v", err)
	}

	tests := []struct {
		name      string
		item      string
		level     Level
		granted   bool
		reasonHas string
	}{
		{name: "an item at a level it allows", item: "deployments.manage", level: LevelTenant, granted: true},
		{name: "an item at the platform level", item: "crds.manage", level: LevelPlatform, granted: true},
		{
			name: "an item at a level it does not allow", item: "crds.manage", level: LevelTenant,
			reasonHas: "not grantable at the tenant level",
		},
		{
			name: "an item nobody defined", item: "secrets.manage", level: LevelPlatform,
			reasonHas: "does not hold",
		},
		{
			name: "a level nobody defined", item: "deployments.manage", level: Level("cluster"),
			reasonHas: "not grantable at the cluster level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item, err := catalogue.Grant(tt.item, tt.level)

			if tt.granted {
				if err != nil {
					t.Fatalf("Grant: %v", err)
				}
				if item.Resources == "" {
					t.Error("the granted item covers nothing")
				}
				return
			}

			if err == nil {
				t.Fatalf("Grant = %+v, want a refusal", item)
			}
			if !strings.Contains(err.Error(), tt.reasonHas) {
				t.Errorf("error = %q, want it to carry %q", err.Error(), tt.reasonHas)
			}
		})
	}
}

// One bad item fails the whole catalogue: a catalogue that was read in part
// would offer fewer permissions than the deployment defines, silently.
func TestParseCatalogueRefusesABadItem(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		reasonHas string
	}{
		{
			name: "an item covering no resources",
			document: `
items:
  deployments.manage:
    resources: ""
    levels: [platform]
`,
			reasonHas: "means every resource",
		},
		{
			name: "an item naming no level",
			document: `
items:
  deployments.manage:
    resources: "deployments"
`,
			reasonHas: "could never be granted",
		},
		{
			name: "an item naming a level nobody defined",
			document: `
items:
  deployments.manage:
    resources: "deployments"
    levels: [cluster]
`,
			reasonHas: `unknown level "cluster"`,
		},
		{
			name: "a catalogue with no items",
			document: `
items: {}
`,
			reasonHas: "holds no items",
		},
		{
			name:      "a document that is not a catalogue",
			document:  "deployments.manage: deployments",
			reasonHas: "reading the",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalogue, err := ParseCatalogue(map[string]string{CatalogueKey: tt.document})
			if err == nil {
				t.Fatalf("ParseCatalogue = %+v, want a refusal", catalogue)
			}
			if !strings.Contains(err.Error(), tt.reasonHas) {
				t.Errorf("error = %q, want it to carry %q", err.Error(), tt.reasonHas)
			}
		})
	}
}

func TestParseCatalogueRefusesAMissingDocument(t *testing.T) {
	for _, data := range []map[string]string{
		nil,
		{},
		{"something-else": "x"},
		{CatalogueKey: "   "},
	} {
		if catalogue, err := ParseCatalogue(data); err == nil {
			t.Errorf("ParseCatalogue(%v) = %+v, want a refusal", data, catalogue)
		}
	}
}

// The level values are the ones the role label carries, so a catalogue level
// and a role's label cannot drift into two vocabularies.
func TestLevelsAreTheRoleLabels(t *testing.T) {
	if LevelPlatform != Level("platform") || LevelTenant != Level("tenant") || LevelProject != Level("project") {
		t.Errorf("levels = %q/%q/%q, want the label values", LevelPlatform, LevelTenant, LevelProject)
	}
}
