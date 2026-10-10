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

package alertconfig

import "sort"

// secretReferences returns the names of the Secrets an AlertmanagerConfig spec
// refers to.
//
// It walks the spec instead of reading typed fields because the credential
// field differs per receiver channel, and the type that describes them lives in
// prometheus-operator, which this repository does not depend on. A map holding
// both a string "name" and a string "key" is a SecretKeySelector: that pair is
// what every receiver's credential is made of, and nothing else in the spec has
// it — a route matcher carries "name" with "value", and a receiver's own "name"
// is a plain string.
func secretReferences(spec interface{}) []string {
	found := map[string]bool{}
	collectSecretReferences(spec, found)
	return sortedNames(found)
}

func collectSecretReferences(value interface{}, found map[string]bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		if name, ok := secretSelectorName(typed); ok {
			found[name] = true
			return
		}
		for _, nested := range typed {
			collectSecretReferences(nested, found)
		}
	case []interface{}:
		for _, nested := range typed {
			collectSecretReferences(nested, found)
		}
	}
}

// secretSelectorName reports whether a map is a SecretKeySelector, and which
// Secret it names.
func secretSelectorName(fields map[string]interface{}) (string, bool) {
	name, named := fields["name"].(string)
	_, keyed := fields["key"].(string)
	if !named || !keyed || name == "" {
		return "", false
	}
	return name, true
}

// sortedNames returns the members of a set in a stable order, so that a
// reconcile does the same work in the same sequence every time.
func sortedNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
