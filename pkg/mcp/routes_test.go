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

package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kubecube-io/kubecube/pkg/agentapi"
	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

// Every tool the catalogue advertises has an endpoint, and no endpoint is for a
// tool nobody can call: a tool added without one fails here rather than in a
// deployment.
func TestEveryToolHasARoute(t *testing.T) {
	routes := RoutesFor()

	for _, tool := range Catalogue() {
		if _, ok := routes[tool.Name]; !ok {
			t.Errorf("tool %q has no route", tool.Name)
		}
	}

	named := make(map[string]bool)
	for _, tool := range Catalogue() {
		named[tool.Name] = true
	}
	for name := range routes {
		if !named[name] {
			t.Errorf("route %q is for a tool the catalogue does not advertise", name)
		}
	}
}

func TestRoutesBuildPaths(t *testing.T) {
	routes := RoutesFor()
	session := readySession()

	tests := []struct {
		tool      appv1.ToolName
		arguments string
		method    string
		path      string
		refused   bool
	}{
		{
			tool: appv1.ToolListGroupResources, method: http.MethodGet,
			path: agentapi.Prefix + "/group",
		},
		{
			tool: appv1.ToolDescribeGroup, method: http.MethodGet,
			path: agentapi.Prefix + "/group/description",
		},
		{
			tool: appv1.ToolGetResource, arguments: `{"kind":"Deployment","name":"web"}`,
			method: http.MethodGet, path: agentapi.Prefix + "/resources/Deployment/web",
		},
		{
			tool: appv1.ToolGetEvents, arguments: `{"kind":"Pod","name":"web-6d4f-x2k"}`,
			method: http.MethodGet, path: agentapi.Prefix + "/resources/Pod/web-6d4f-x2k/events",
		},
		{
			tool: appv1.ToolGetLogs, arguments: `{"kind":"Pod","name":"web-6d4f-x2k"}`,
			method: http.MethodGet, path: agentapi.Prefix + "/resources/Pod/web-6d4f-x2k/logs",
		},
		{
			tool: appv1.ToolRenderMember, arguments: `{"kind":"Deployment","name":"web"}`,
			method: http.MethodPost, path: agentapi.Prefix + "/group/render",
		},
		{
			tool: appv1.ToolValidateChange, arguments: `{"manifests":[]}`,
			method: http.MethodPost, path: agentapi.Prefix + "/sandbox/validate",
		},
		{
			tool: appv1.ToolListChartVersions, arguments: `{"chart":"shop"}`,
			method: http.MethodGet, path: agentapi.Prefix + "/charts/shop/versions",
		},
		{
			tool: appv1.ToolSandboxApply, arguments: `{"manifests":[]}`,
			method: http.MethodPost, path: agentapi.Prefix + "/sandbox/changesets",
		},
		{
			tool: appv1.ToolSandboxRollback, arguments: `{"name":"change-0001"}`,
			method: http.MethodPost, path: agentapi.Prefix + "/sandbox/changesets/change-0001/rollback",
		},
		{
			tool: appv1.ToolRunSmokeTest, arguments: `{"image":"busybox"}`,
			method: http.MethodPost, path: agentapi.Prefix + "/sandbox/smoke-tests",
		},
		{
			tool: appv1.ToolDestroySandbox, method: http.MethodDelete,
			path: agentapi.Prefix + "/sandbox",
		},
		{
			tool: appv1.ToolProposeChange, arguments: `{"summary":"raise the replicas"}`,
			method: http.MethodPost, path: agentapi.Prefix + "/change-requests",
		},
		{
			tool: appv1.ToolGetResource, arguments: `{"kind":"Deployment","name":"a/b"}`,
			method: http.MethodGet, path: agentapi.Prefix + "/resources/Deployment/a%2Fb",
		},
		{
			tool: appv1.ToolGetResource, arguments: `{"kind":"Deployment","name":""}`,
			refused: true,
		},
		{
			tool: appv1.ToolGetResource, refused: true,
		},
		{
			tool: appv1.ToolSandboxRollback, arguments: `{}`,
			refused: true,
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.tool)+" "+tt.arguments, func(t *testing.T) {
			route, ok := routes[string(tt.tool)]
			if !ok {
				t.Fatalf("no route for %q", tt.tool)
			}

			path, err := route.Path(session, json.RawMessage(tt.arguments))
			if tt.refused {
				if err == nil {
					t.Fatalf("path = %q, want a refusal", path)
				}
				return
			}

			if err != nil {
				t.Fatalf("Path: %v", err)
			}
			if path != tt.path {
				t.Errorf("path = %q, want %q", path, tt.path)
			}
			if route.Method != tt.method {
				t.Errorf("method = %q, want %q", route.Method, tt.method)
			}
		})
	}
}

// Nothing may address a cluster, a namespace or a group: the session's token
// says which group a call is about, and a path that carried one could be
// pointed at somebody else's.
func TestRoutesStayInsideTheSessionScopedPrefix(t *testing.T) {
	for name, route := range RoutesFor() {
		path, err := route.Path(readySession(), json.RawMessage(`{"kind":"Deployment","name":"web","chart":"shop","name":"change-0001"}`))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if !strings.HasPrefix(path, agentapi.Prefix+"/") {
			t.Errorf("%s: path %q leaves the prefix %q", name, path, agentapi.Prefix)
		}
		for _, forbidden := range []string{"namespace", "cluster", "/groups/", "kubecube-project"} {
			if strings.Contains(path, forbidden) {
				t.Errorf("%s: path %q names %q, which the token is supposed to say", name, path, forbidden)
			}
		}
	}
}

// A tool that only reads must not be reachable by a method that writes.
func TestReadToolsOnlyRead(t *testing.T) {
	for _, tool := range Catalogue() {
		if tool.Tier != TierRead {
			continue
		}
		if route := RoutesFor()[tool.Name]; route.Method != http.MethodGet {
			t.Errorf("read tool %q is served by %s", tool.Name, route.Method)
		}
	}
}
