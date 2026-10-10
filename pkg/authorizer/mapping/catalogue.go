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
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// CatalogueKey is the ConfigMap key the catalogue is shipped under.
const CatalogueKey = "catalogue"

// Level is where a permission may be granted.
//
// Its values are the ones the kubecube.io/role label already carries, so the
// catalogue, the label and the console's three scopes are one vocabulary rather
// than three that have to be kept in step.
type Level string

const (
	// LevelPlatform is the whole platform.
	LevelPlatform Level = constants.ClusterRolePlatform

	// LevelTenant is one tenant.
	LevelTenant Level = constants.ClusterRoleTenant

	// LevelProject is one project.
	LevelProject Level = constants.ClusterRoleProject
)

// LevelOf reads the level a request named.
//
// The level comes from the request and never from the role's own label: a label
// is what the server writes down afterwards, and a read that trusted it would
// decide what a caller may do from something the caller's role happens to say.
func LevelOf(scope string) (Level, error) {
	level := Level(strings.TrimSpace(scope))
	if !level.known() {
		return "", fmt.Errorf("the scope %q is not one of %s, %s or %s", scope, LevelPlatform, LevelTenant, LevelProject)
	}
	return level, nil
}

// Item is one grantable permission.
type Item struct {
	// Resources the item covers, separated by semicolons, exactly as the
	// ClusterRole rule will carry them.
	Resources string `json:"resources"`

	// Levels the item may be granted at.
	Levels []Level `json:"levels"`
}

// ResourceNames is what the item covers, as RBAC names one resource at a time.
func (i Item) ResourceNames() []string {
	names := make([]string, 0, strings.Count(i.Resources, ";")+1)
	for _, name := range strings.Split(i.Resources, ";") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// Catalogue is what may be granted, and where.
//
// It is the server's answer to "which permissions exist". A role says which of
// these it holds; it never says which ones exist, because a client that can
// introduce an item can introduce a permission nobody defined.
type Catalogue struct {
	Items map[string]Item `json:"items"`
}

// ParseCatalogue reads the catalogue out of the ConfigMap the chart ships.
//
// Every item is checked as it is read, and one bad item fails the whole
// catalogue: a partially read catalogue would silently offer fewer permissions
// than the deployment defines, which is the failure this type exists to stop.
func ParseCatalogue(data map[string]string) (*Catalogue, error) {
	document, ok := data[CatalogueKey]
	if !ok || strings.TrimSpace(document) == "" {
		return nil, fmt.Errorf("the configmap carries no %s", CatalogueKey)
	}

	catalogue := &Catalogue{}
	// Strict, because this document is the deployment's own definition: a
	// mistyped key would otherwise be ignored, and the catalogue would come back
	// holding fewer permissions than it was written with.
	if err := yaml.UnmarshalStrict([]byte(document), catalogue); err != nil {
		return nil, fmt.Errorf("reading the %s: %w", CatalogueKey, err)
	}
	if len(catalogue.Items) == 0 {
		return nil, fmt.Errorf("the %s holds no items, so nothing could be granted", CatalogueKey)
	}

	for name, item := range catalogue.Items {
		if err := item.check(name); err != nil {
			return nil, err
		}
	}

	return catalogue, nil
}

// Names is every item the catalogue holds, sorted, which is the list a console
// renders.
func (c *Catalogue) Names() []string {
	names := make([]string, 0, len(c.Items))
	for name := range c.Items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Grant answers whether one item may be granted at one level, and what it
// covers when it may.
//
// Both refusals are errors rather than empty answers: an item the catalogue does
// not hold and an item at a level that does not allow it are the two ways a
// request can ask for a permission nobody defined, and the caller has to be able
// to refuse the request rather than write a smaller rule.
func (c *Catalogue) Grant(item string, level Level) (Item, error) {
	found, ok := c.Items[item]
	if !ok {
		return Item{}, fmt.Errorf("the catalogue does not hold %q, so it cannot be granted", item)
	}

	for _, allowed := range found.Levels {
		if allowed == level {
			return found, nil
		}
	}

	return Item{}, fmt.Errorf("item %s is not grantable at the %s level", item, level)
}

// FlatItemsAt is the catalogue as the readers that predate it see it: item →
// its resources, separated by semicolons, holding only the items grantable at
// one of the given levels.
//
// It is how the definition moved without a flag day. The readers take a level's
// view of one catalogue instead of one of two tables, so an item is visible
// exactly where its own levels allow it — which is the same answer the two
// tables gave, now derived from one source rather than maintained as two.
func (c *Catalogue) FlatItemsAt(levels ...Level) map[string]string {
	items := make(map[string]string, len(c.Items))

	for name, item := range c.Items {
		for _, allowed := range item.Levels {
			if containsLevel(levels, allowed) {
				items[name] = item.Resources
				break
			}
		}
	}

	return items
}

func containsLevel(levels []Level, wanted Level) bool {
	for _, level := range levels {
		if level == wanted {
			return true
		}
	}
	return false
}

func (i Item) check(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("the %s holds an item with no name", CatalogueKey)
	}
	if len(i.ResourceNames()) == 0 {
		return fmt.Errorf("item %s covers no resources, and a rule without resources means every resource", name)
	}
	if len(i.Levels) == 0 {
		return fmt.Errorf("item %s names no level, so it could never be granted", name)
	}
	for _, level := range i.Levels {
		if !level.known() {
			return fmt.Errorf("item %s names the unknown level %q", name, level)
		}
	}
	return nil
}

func (l Level) known() bool {
	switch l {
	case LevelPlatform, LevelTenant, LevelProject:
		return true
	default:
		return false
	}
}
