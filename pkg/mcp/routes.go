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
	"fmt"
	"net/http"

	"github.com/kubecube-io/kubecube/pkg/agentapi"
	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

// RoutesFor is the tool-to-endpoint table: where each tool's call goes on the
// platform.
//
// It is written once, beside the catalogue whose names it keys on, so a tool
// added without an endpoint fails a test rather than a deployment. Every path
// comes from pkg/agentapi, which is the contract the platform's handlers are
// written against: neither half spells a path of its own.
//
// A deployment must not enable these before the platform serves them. Until it
// does, a call would come back as a missing object rather than as an endpoint
// nobody wrote, and the first of those reads like a scope decision.
func RoutesFor() map[string]Route {
	return map[string]Route{
		string(appv1.ToolListGroupResources): {
			Method: http.MethodGet,
			Path:   constantPath(agentapi.Group),
		},
		string(appv1.ToolDescribeGroup): {
			Method: http.MethodGet,
			Path:   constantPath(agentapi.GroupDescription),
		},
		string(appv1.ToolGetResource): objectRoute(http.MethodGet, agentapi.Resource),
		string(appv1.ToolGetEvents):   objectRoute(http.MethodGet, agentapi.ResourceEvents),
		string(appv1.ToolGetLogs):     objectRoute(http.MethodGet, agentapi.ResourceLogs),
		// Rendering names the member in the call's arguments rather than in its
		// path: it is a question about an object, not a read of one.
		string(appv1.ToolRenderMember): {
			Method: http.MethodPost,
			Path:   constantPath(agentapi.RenderMember),
		},
		string(appv1.ToolValidateChange): {
			Method: http.MethodPost,
			Path:   constantPath(agentapi.ValidateChange),
		},
		string(appv1.ToolListChartVersions): {
			Method: http.MethodGet,
			Path: func(_ Session, arguments json.RawMessage) (string, error) {
				named := struct {
					Chart string `json:"chart"`
				}{}
				if err := decodeArguments(arguments, &named); err != nil {
					return "", err
				}
				return agentapi.ChartVersions(named.Chart)
			},
		},
		string(appv1.ToolSandboxApply): {
			Method: http.MethodPost,
			Path:   constantPath(agentapi.ChangeSets),
		},
		string(appv1.ToolSandboxRollback): {
			Method: http.MethodPost,
			Path: func(_ Session, arguments json.RawMessage) (string, error) {
				named := struct {
					Name string `json:"name"`
				}{}
				if err := decodeArguments(arguments, &named); err != nil {
					return "", err
				}
				return agentapi.ChangeSetRollback(named.Name)
			},
		},
		string(appv1.ToolRunSmokeTest): {
			Method: http.MethodPost,
			Path:   constantPath(agentapi.SmokeTests),
		},
		string(appv1.ToolDestroySandbox): {
			Method: http.MethodDelete,
			Path:   constantPath(agentapi.Sandbox),
		},
		string(appv1.ToolProposeChange): {
			Method: http.MethodPost,
			Path:   constantPath(agentapi.ChangeRequests),
		},
	}
}

// constantPath is a route whose path does not depend on the call.
func constantPath(build func() string) func(Session, json.RawMessage) (string, error) {
	return func(Session, json.RawMessage) (string, error) { return build(), nil }
}

// objectRoute is a route that names one object inside the group's namespace.
func objectRoute(method string, build func(kind, name string) (string, error)) Route {
	return Route{
		Method: method,
		Path: func(_ Session, arguments json.RawMessage) (string, error) {
			ref := agentapi.ObjectRef{}
			if err := decodeArguments(arguments, &ref); err != nil {
				return "", err
			}
			return build(ref.Kind, ref.Name)
		},
	}
}

// decodeArguments reads a call's arguments into the shape one route expects. An
// absent argument is an error rather than an empty value, because a path built
// from an empty value addresses something other than what the caller meant.
func decodeArguments(arguments json.RawMessage, into interface{}) error {
	if len(arguments) == 0 {
		return fmt.Errorf("the call carries no arguments")
	}
	if err := json.Unmarshal(arguments, into); err != nil {
		return fmt.Errorf("the call's arguments could not be read: %w", err)
	}
	return nil
}
