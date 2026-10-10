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
	"testing"

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

// The tool names exist twice: in the API types, which the platform writes and
// reads, and on this surface, which advertises them. This test is what keeps
// them one vocabulary — a rename on either side fails here instead of silently
// never matching a session's allowlist, which is a mismatch nobody would see
// until a tool call was refused for no visible reason.
//
// The session modes needed no such test: this surface uses the API's own type
// for them rather than a copy of its values, so there is nothing to disagree.
func TestCatalogueMatchesTheAPIToolNames(t *testing.T) {
	api := map[string]bool{
		string(appv1.ToolListGroupResources): true,
		string(appv1.ToolGetResource):        true,
		string(appv1.ToolGetEvents):          true,
		string(appv1.ToolGetLogs):            true,
		string(appv1.ToolDescribeGroup):      true,
		string(appv1.ToolRenderMember):       true,
		string(appv1.ToolValidateChange):     true,
		string(appv1.ToolListChartVersions):  true,
		string(appv1.ToolSandboxApply):       true,
		string(appv1.ToolSandboxRollback):    true,
		string(appv1.ToolRunSmokeTest):       true,
		string(appv1.ToolDestroySandbox):     true,
		string(appv1.ToolProposeChange):      true,
	}

	for _, tool := range Catalogue() {
		if !api[tool.Name] {
			t.Errorf("this surface advertises %q, which the API does not name", tool.Name)
			continue
		}
		delete(api, tool.Name)
	}

	for name := range api {
		t.Errorf("the API names %q, which this surface does not advertise", name)
	}
}
