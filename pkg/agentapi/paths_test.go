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

package agentapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kubecube-io/kubecube/pkg/resourcegroup"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

func TestPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "the group", path: Group(), want: constants.ApiPathRoot + "/agent/group"},
		{name: "the group's description", path: GroupDescription(), want: constants.ApiPathRoot + "/agent/group/description"},
		{name: "rendering a member", path: RenderMember(), want: constants.ApiPathRoot + "/agent/group/render"},
		{name: "validating a change", path: ValidateChange(), want: constants.ApiPathRoot + "/agent/sandbox/validate"},
		{name: "the sandbox", path: Sandbox(), want: constants.ApiPathRoot + "/agent/sandbox"},
		{name: "the change sets", path: ChangeSets(), want: constants.ApiPathRoot + "/agent/sandbox/changesets"},
		{name: "the smoke tests", path: SmokeTests(), want: constants.ApiPathRoot + "/agent/sandbox/smoke-tests"},
		{name: "the change requests", path: ChangeRequests(), want: constants.ApiPathRoot + "/agent/change-requests"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.path != tt.want {
				t.Errorf("path = %q, want %q", tt.path, tt.want)
			}
			if !strings.HasPrefix(tt.path, Prefix+"/") {
				t.Errorf("path %q leaves the prefix %q", tt.path, Prefix)
			}
		})
	}
}

func TestObjectPaths(t *testing.T) {
	resource, err := Resource("Deployment", "web")
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}
	if resource != Prefix+"/resources/Deployment/web" {
		t.Errorf("Resource = %q", resource)
	}

	events, err := ResourceEvents("Pod", "web-6d4f-x2k")
	if err != nil {
		t.Fatalf("ResourceEvents: %v", err)
	}
	if events != Prefix+"/resources/Pod/web-6d4f-x2k/events" {
		t.Errorf("ResourceEvents = %q", events)
	}

	logs, err := ResourceLogs("Pod", "web-6d4f-x2k")
	if err != nil {
		t.Fatalf("ResourceLogs: %v", err)
	}
	if !strings.HasSuffix(logs, "/logs") {
		t.Errorf("ResourceLogs = %q, want it to end in the log route", logs)
	}
}

// A name is escaped rather than pasted: a name with a slash in it would
// otherwise add a segment and address something else entirely.
func TestPathsEscapeWhatTheyCarry(t *testing.T) {
	resource, err := Resource("Deployment", "a/b")
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}
	if resource != Prefix+"/resources/Deployment/a%2Fb" {
		t.Errorf("Resource = %q, want the name escaped", resource)
	}

	rollback, err := ChangeSetRollback("change 0001")
	if err != nil {
		t.Fatalf("ChangeSetRollback: %v", err)
	}
	if rollback != Prefix+"/sandbox/changesets/change%200001/rollback" {
		t.Errorf("ChangeSetRollback = %q, want the name escaped", rollback)
	}
}

// A path built without a segment addresses a collection where the caller meant
// an object, so an empty one is refused rather than dropped.
func TestPathsRefuseAnEmptySegment(t *testing.T) {
	if path, err := Resource("", "web"); err == nil {
		t.Errorf("Resource = %q, want a refusal", path)
	}
	if path, err := Resource("Deployment", ""); err == nil {
		t.Errorf("Resource = %q, want a refusal", path)
	}
	if path, err := ChartVersions(""); err == nil {
		t.Errorf("ChartVersions = %q, want a refusal", path)
	}
	if path, err := ChangeSetRollback(""); err == nil {
		t.Errorf("ChangeSetRollback = %q, want a refusal", path)
	}
}

// The field names are the contract: the surface decodes what the platform
// writes, and a rename on one side would otherwise be a field that silently
// stays empty.
func TestAnswersSpellTheirFields(t *testing.T) {
	encoded, err := json.Marshal(ValidateAnswer{
		DryRun:         resourcegroup.ResultPassed,
		Policy:         resourcegroup.ResultPassed,
		SmokeTest:      resourcegroup.ResultPassed,
		QuotaEstimated: true,
		Notes:          []string{"a note"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	for _, field := range []string{`"dryRun"`, `"policy"`, `"smokeTest"`, `"quotaEstimated"`, `"notes"`} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("the answer %s does not carry %s", encoded, field)
		}
	}

	answer := ValidateAnswer{}
	if err := json.Unmarshal(encoded, &answer); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	evidence := answer.Evidence()
	if evidence.DryRun != resourcegroup.ResultPassed || !evidence.QuotaEstimated {
		t.Errorf("evidence = %+v, want what was written", evidence)
	}
}

func TestObjectRefSpellsItsFields(t *testing.T) {
	encoded, err := json.Marshal(ObjectRef{Kind: "Deployment", Name: "web"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != `{"kind":"Deployment","name":"web"}` {
		t.Errorf("ObjectRef = %s", encoded)
	}
}
