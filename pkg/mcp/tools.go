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

// Package mcp is the surface an external agent reaches the platform through:
// the tools the server advertises, the scope a session's token carries, and the
// JSON-RPC dispatch between them.
//
// It holds no cluster credential and makes no authorization decision of its
// own. The scope gate below narrows what a session may ask for; the platform
// decides what may happen, and the sandbox decides where it may happen. That
// division is the point: a surface that could widen a scope would be the whole
// boundary, and one that only narrows it can be replaced without moving the
// boundary.
package mcp

// Tier is what a tool does, and therefore who may call it. The tiers exist so
// that a session's mode can be enforced without the server knowing what each
// tool is for.
type Tier string

const (
	// TierRead reads what the group holds.
	TierRead Tier = "read"

	// TierPropose renders and validates a change without applying it.
	TierPropose Tier = "propose"

	// TierSandboxWrite applies inside the session's sandbox. There is no tier
	// that writes to a production group: a change reaches one only when a human
	// lands it through the channel that already owns those resources.
	TierSandboxWrite Tier = "sandbox-write"

	// TierSubmit hands a validated change to a human.
	TierSubmit Tier = "submit"
)

// Tool is one tool the server advertises.
type Tool struct {
	Name        string
	Tier        Tier
	Description string
	InputSchema map[string]interface{}
}

// Catalogue is every tool, in the order a client sees them.
//
// The names and tiers are the design's; the input schemas are the initial
// shape of the contract. Every schema closes itself with
// additionalProperties: false, because an argument the server silently ignores
// is an argument the caller believed had an effect.
func Catalogue() []Tool {
	return []Tool{
		{
			Name:        "list_group_resources",
			Tier:        TierRead,
			Description: "List the objects in this session's group, declared and derived.",
			InputSchema: schema(nil, nil),
		},
		{
			Name:        "get_resource",
			Tier:        TierRead,
			Description: "Read one object in the group.",
			InputSchema: schema(map[string]interface{}{
				"kind":       stringProp("Kubernetes kind, for example Deployment."),
				"name":       stringProp("Name of the object."),
				"apiVersion": stringProp("API version of the object, for example apps/v1."),
			}, []string{"kind", "name"}),
		},
		{
			Name:        "get_events",
			Tier:        TierRead,
			Description: "Read the events recorded for one object in the group.",
			InputSchema: schema(map[string]interface{}{
				"kind": stringProp("Kubernetes kind of the object."),
				"name": stringProp("Name of the object."),
			}, []string{"kind", "name"}),
		},
		{
			Name:        "get_logs",
			Tier:        TierRead,
			Description: "Read the logs of one container in the group.",
			InputSchema: schema(map[string]interface{}{
				"pod":       stringProp("Name of the pod."),
				"container": stringProp("Container name. The pod's only container is used when it is omitted."),
				"tailLines": integerProp("How many lines to return from the end."),
			}, []string{"pod"}),
		},
		{
			Name:        "describe_group",
			Tier:        TierRead,
			Description: "Describe the group: its phase, its members, the environment it sits in and what a session may do with it.",
			InputSchema: schema(nil, nil),
		},
		{
			Name:        "list_chart_versions",
			Tier:        TierPropose,
			Description: "List the versions of a chart this group may use.",
			InputSchema: schema(map[string]interface{}{
				"chart": stringProp("Chart name."),
			}, []string{"chart"}),
		},
		{
			Name:        "render_member",
			Tier:        TierPropose,
			Description: "Render one member with proposed values, without applying anything.",
			InputSchema: schema(map[string]interface{}{
				"member": stringProp("Member to render."),
				"values": objectProp("Values to render it with."),
			}, []string{"member"}),
		},
		{
			Name:        "validate_change",
			Tier:        TierPropose,
			Description: "Validate a change: server-side dry-run, the policy check, a quota estimate and a diff against what is deployed.",
			InputSchema: schema(map[string]interface{}{
				"manifests": arrayProp("The manifests to validate, as YAML documents."),
			}, []string{"manifests"}),
		},
		{
			Name:        "sandbox_apply",
			Tier:        TierSandboxWrite,
			Description: "Apply manifests into this session's sandbox. It never reaches a production group.",
			InputSchema: schema(map[string]interface{}{
				"manifests": arrayProp("The manifests to apply, as YAML documents."),
			}, []string{"manifests"}),
		},
		{
			Name:        "sandbox_rollback",
			Tier:        TierSandboxWrite,
			Description: "Return the sandbox to a change set it applied earlier.",
			InputSchema: schema(map[string]interface{}{
				"changeSet": stringProp("Change set to re-apply, as returned by sandbox_apply."),
			}, []string{"changeSet"}),
		},
		{
			Name:        "run_smoke_test",
			Tier:        TierSandboxWrite,
			Description: "Run a command in the sandbox as a Job and return its logs and exit code.",
			InputSchema: schema(map[string]interface{}{
				"command": arrayProp("Command to run."),
				"image":   stringProp("Image to run it in."),
			}, []string{"command"}),
		},
		{
			Name:        "destroy_sandbox",
			Tier:        TierSandboxWrite,
			Description: "Delete this session's sandbox. The session ends with it.",
			InputSchema: schema(nil, nil),
		},
		{
			Name:        "propose_change",
			Tier:        TierSubmit,
			Description: "Hand a validated change to a human. The platform never lands it itself.",
			InputSchema: schema(map[string]interface{}{
				"summary": stringProp("What the change does, for the person reviewing it."),
			}, []string{"summary"}),
		},
	}
}

// Lookup finds a tool by name.
func Lookup(name string) (Tool, bool) {
	for _, tool := range Catalogue() {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

func schema(properties map[string]interface{}, required []string) map[string]interface{} {
	if properties == nil {
		properties = map[string]interface{}{}
	}

	s := map[string]interface{}{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func stringProp(description string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": description}
}

func integerProp(description string) map[string]interface{} {
	return map[string]interface{}{"type": "integer", "description": description}
}

func objectProp(description string) map[string]interface{} {
	return map[string]interface{}{"type": "object", "description": description}
}

func arrayProp(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": description,
		"items":       map[string]interface{}{"type": "string"},
	}
}
