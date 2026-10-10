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

// The level's view of one catalogue replaces the two tables: an item appears
// where its own levels allow it, and nowhere else.
func TestFlatItemsAt(t *testing.T) {
	catalogue, err := ParseCatalogue(map[string]string{CatalogueKey: catalogueDocument})
	if err != nil {
		t.Fatalf("ParseCatalogue: %v", err)
	}

	platform := catalogue.FlatItemsAt(LevelPlatform)
	if _, ok := platform["crds.manage"]; !ok {
		t.Error("crds.manage is missing from the platform view, and the catalogue grants it there")
	}
	if _, ok := platform["deployments.manage"]; !ok {
		t.Error("deployments.manage is missing from the platform view, and the catalogue grants it there")
	}

	// What a tenant or a project sees: the same items minus the platform's own.
	tenantAndProject := catalogue.FlatItemsAt(LevelTenant, LevelProject)
	if _, ok := tenantAndProject["crds.manage"]; ok {
		t.Error("crds.manage is visible at the tenant or project level, and the catalogue does not grant it there")
	}
	if _, ok := tenantAndProject["deployments.manage"]; !ok {
		t.Error("deployments.manage is missing, and the catalogue grants it at both levels")
	}

	if len(platform) != 2 || len(tenantAndProject) != 1 {
		t.Errorf("views hold %d and %d items, want 2 and 1", len(platform), len(tenantAndProject))
	}

	// The values stay in the shape the readers already split on.
	if !strings.Contains(tenantAndProject["deployments.manage"], ";") {
		t.Errorf("resources = %q, want the semicolon-separated form", tenantAndProject["deployments.manage"])
	}
}

func TestFlatItemsAtALevelNothingIsGrantedAt(t *testing.T) {
	catalogue, err := ParseCatalogue(map[string]string{CatalogueKey: catalogueDocument})
	if err != nil {
		t.Fatalf("ParseCatalogue: %v", err)
	}

	if items := catalogue.FlatItemsAt(Level("cluster")); len(items) != 0 {
		t.Errorf("items = %v, want nothing at a level no item names", items)
	}
	if items := catalogue.FlatItemsAt(); len(items) != 0 {
		t.Errorf("items = %v, want nothing when no level is asked for", items)
	}
}
