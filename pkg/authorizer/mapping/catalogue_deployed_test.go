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
	"os"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestTheDeployedCatalogueParses reads the catalogue the chart actually ships,
// not a fixture that resembles it.
//
// The chart is a sibling repository, so CI — which checks out this one alone —
// has no copy of it and this test skips there. It is what a person runs after
// changing the catalogue:
//
//	helm template kubecube ../kubecube-chart \
//	  -s templates/kubecube/auth-mapping-configmap.yaml \
//	  --set-string global.componentsEnable.kubecube=true \
//	  > auth-mapping.yaml
//	KUBECUBE_CATALOGUE_FILE=auth-mapping.yaml go test ./pkg/authorizer/mapping/
//
// It is the only check that catches the two halves drifting: the parser's own
// tests would keep passing while the rendered catalogue stopped being readable.
func TestTheDeployedCatalogueParses(t *testing.T) {
	path := os.Getenv("KUBECUBE_CATALOGUE_FILE")
	if path == "" {
		t.Skip("set KUBECUBE_CATALOGUE_FILE to the ConfigMap the chart renders")
	}

	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	rendered := struct {
		APIVersion string                 `json:"apiVersion"`
		Kind       string                 `json:"kind"`
		Metadata   map[string]interface{} `json:"metadata"`
		Data       map[string]string      `json:"data"`
	}{}
	if err := yaml.UnmarshalStrict(document, &rendered); err != nil {
		t.Fatalf("reading the rendered ConfigMap: %v", err)
	}
	if rendered.Kind != "ConfigMap" {
		t.Fatalf("the rendered document is a %s, want the catalogue's ConfigMap", rendered.Kind)
	}

	catalogue, err := ParseCatalogue(rendered.Data)
	if err != nil {
		t.Fatalf("the deployed catalogue does not parse: %v", err)
	}

	// The items whose levels are the whole point of the change.
	if _, err := catalogue.Grant("deployments.manage", LevelProject); err != nil {
		t.Errorf("deployments.manage is not grantable at the project level: %v", err)
	}
	if _, err := catalogue.Grant("crds.manage", LevelPlatform); err != nil {
		t.Errorf("crds.manage is not grantable at the platform level: %v", err)
	}
	if _, err := catalogue.Grant("crds.manage", LevelTenant); err == nil {
		t.Error("crds.manage is grantable at the tenant level, and the catalogue says it is the platform's alone")
	}
	if _, err := catalogue.Grant("resourcegroups.scope", LevelProject); err != nil {
		t.Errorf("resourcegroups.scope is not grantable at the project level: %v", err)
	}

	// Every item the console offers has to be grantable somewhere, or it is a
	// checkbox that can never be ticked.
	for _, name := range catalogue.Names() {
		item := catalogue.Items[name]
		granted := false
		for _, level := range item.Levels {
			if _, err := catalogue.Grant(name, level); err == nil {
				granted = true
			}
		}
		if !granted {
			t.Errorf("item %s names levels, none of which grant it", name)
		}
	}
}
