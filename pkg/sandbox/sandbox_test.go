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

package sandbox

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const sandboxNS = "kubecube-agent-sess-7f3a"

func obj(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
	}}
}

func withContainer(o *unstructured.Unstructured, container map[string]interface{}) *unstructured.Unstructured {
	var spec map[string]interface{}
	if o.GetKind() == "Pod" {
		spec = map[string]interface{}{"containers": []interface{}{container}}
		_ = unstructured.SetNestedMap(o.Object, spec, "spec")
		return o
	}
	if o.GetKind() == "CronJob" {
		_ = unstructured.SetNestedMap(o.Object, map[string]interface{}{
			"jobTemplate": map[string]interface{}{
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"spec": map[string]interface{}{"containers": []interface{}{container}},
					},
				},
			},
		}, "spec")
		return o
	}
	spec = map[string]interface{}{"containers": []interface{}{container}}
	_ = unstructured.SetNestedMap(o.Object, map[string]interface{}{"spec": spec}, "spec", "template")
	return o
}

func deployment(container map[string]interface{}) unstructured.Unstructured {
	o := obj("apps/v1", "Deployment", sandboxNS, "web")
	withContainer(o, container)
	return *o
}

func plainContainer() map[string]interface{} {
	return map[string]interface{}{"name": "app", "image": "registry.internal/demo/web:1.2.3"}
}

func TestPolicyAllowsAnOrdinaryWorkload(t *testing.T) {
	p := Policy{Namespace: sandboxNS}
	if v := p.Check([]unstructured.Unstructured{deployment(plainContainer())}); len(v) != 0 {
		t.Fatalf("violations = %v, want none", v)
	}
}

func TestPolicyRefusesClusterScopedKinds(t *testing.T) {
	p := Policy{Namespace: sandboxNS}

	for _, o := range []unstructured.Unstructured{
		*obj("rbac.authorization.k8s.io/v1", "ClusterRole", "", "reader"),
		*obj("v1", "Namespace", "", "someone-elses"),
		*obj("apiextensions.k8s.io/v1", "CustomResourceDefinition", "", "widgets.example.com"),
	} {
		v := p.Check([]unstructured.Unstructured{o})
		if len(v) != 1 || !strings.Contains(v[0].Reason, "cluster-scoped") {
			t.Errorf("%s: violations = %v, want one cluster-scoped refusal", o.GetKind(), v)
		}
	}
}

func TestPolicyRefusesAnotherNamespace(t *testing.T) {
	p := Policy{Namespace: sandboxNS}
	o := *obj("v1", "ConfigMap", "kubecube-project-demo-space1", "settings")

	v := p.Check([]unstructured.Unstructured{o})
	if len(v) != 1 || !strings.Contains(v[0].Reason, "instead of the sandbox namespace") {
		t.Fatalf("violations = %v, want a namespace refusal", v)
	}
}

// A manifest that names no namespace is the ordinary case — it is what an
// export of a live object looks like — and the applier fills it in, so the
// policy must not refuse it.
func TestPolicyAllowsAManifestWithoutANamespace(t *testing.T) {
	p := Policy{Namespace: sandboxNS}
	o := *obj("v1", "ConfigMap", "", "exported")

	if v := p.Check([]unstructured.Unstructured{o}); len(v) != 0 {
		t.Fatalf("violations = %v, want a manifest without a namespace allowed", v)
	}
}

func TestPolicyRefusesPrivilegedAndHostAccess(t *testing.T) {
	tests := []struct {
		name      string
		container map[string]interface{}
		want      string
	}{
		{
			name:      "privileged container",
			container: map[string]interface{}{"name": "app", "image": "demo:1", "securityContext": map[string]interface{}{"privileged": true}},
			want:      "privileged container",
		},
		{
			name:      "privilege escalation",
			container: map[string]interface{}{"name": "app", "image": "demo:1", "securityContext": map[string]interface{}{"allowPrivilegeEscalation": true}},
			want:      "privilege escalation",
		},
	}

	p := Policy{Namespace: sandboxNS}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := p.Check([]unstructured.Unstructured{deployment(tt.container)})
			if len(v) != 1 || !strings.Contains(v[0].Reason, tt.want) {
				t.Fatalf("violations = %v, want a refusal mentioning %q", v, tt.want)
			}
		})
	}
}

func TestPolicyRefusesHostNetworkAndHostPath(t *testing.T) {
	p := Policy{Namespace: sandboxNS}

	hostNetwork := deployment(plainContainer())
	_ = unstructured.SetNestedField(hostNetwork.Object, true, "spec", "template", "spec", "hostNetwork")
	if v := p.Check([]unstructured.Unstructured{hostNetwork}); len(v) != 1 || !strings.Contains(v[0].Reason, "hostNetwork") {
		t.Errorf("hostNetwork violations = %v, want a refusal", v)
	}

	hostPath := deployment(plainContainer())
	_ = unstructured.SetNestedSlice(hostPath.Object, []interface{}{
		map[string]interface{}{"name": "data", "hostPath": map[string]interface{}{"path": "/var/lib/data"}},
	}, "spec", "template", "spec", "volumes")
	if v := p.Check([]unstructured.Unstructured{hostPath}); len(v) != 1 || !strings.Contains(v[0].Reason, "hostPath") {
		t.Errorf("hostPath violations = %v, want a refusal", v)
	}
}

func TestPolicyChecksThePodTemplateOfACronJob(t *testing.T) {
	o := obj("batch/v1", "CronJob", sandboxNS, "nightly")
	withContainer(o, map[string]interface{}{
		"name":            "job",
		"image":           "demo:1",
		"securityContext": map[string]interface{}{"privileged": true},
	})

	p := Policy{Namespace: sandboxNS}
	if v := p.Check([]unstructured.Unstructured{*o}); len(v) != 1 {
		t.Fatalf("violations = %v, want the CronJob's pod template to be checked", v)
	}
}

func TestPolicyRegistryAllowlist(t *testing.T) {
	p := Policy{Namespace: sandboxNS, AllowedRegistries: []string{"registry.internal"}}

	if v := p.Check([]unstructured.Unstructured{deployment(plainContainer())}); len(v) != 0 {
		t.Errorf("violations = %v, want the allowed registry to pass", v)
	}

	elsewhere := deployment(map[string]interface{}{"name": "app", "image": "docker.io/library/nginx:1.25"})
	v := p.Check([]unstructured.Unstructured{elsewhere})
	if len(v) != 1 || !strings.Contains(v[0].Reason, "registry") {
		t.Errorf("violations = %v, want a registry refusal", v)
	}
}

func TestPolicyDenylistCanOnlyNarrow(t *testing.T) {
	p := Policy{
		Namespace:   sandboxNS,
		DeniedKinds: []schema.GroupKind{{Group: "", Kind: "ConfigMap"}},
	}

	o := *obj("v1", "ConfigMap", sandboxNS, "settings")
	v := p.Check([]unstructured.Unstructured{o})
	if len(v) != 1 || !strings.Contains(v[0].Reason, "denylist") {
		t.Fatalf("violations = %v, want a denylist refusal", v)
	}
}

func TestPolicyReportsEveryViolation(t *testing.T) {
	p := Policy{Namespace: sandboxNS}
	v := p.Check([]unstructured.Unstructured{
		*obj("rbac.authorization.k8s.io/v1", "ClusterRole", "", "reader"),
		*obj("v1", "ConfigMap", "elsewhere", "settings"),
	})
	if len(v) != 2 {
		t.Fatalf("violations = %v, want both objects reported in one pass", v)
	}
}

func TestSanitizeDropsServerOwnedFields(t *testing.T) {
	o := *obj("apps/v1", "Deployment", sandboxNS, "web")
	_ = unstructured.SetNestedMap(o.Object, map[string]interface{}{"replicas": int64(3)}, "spec")
	_ = unstructured.SetNestedMap(o.Object, map[string]interface{}{"readyReplicas": int64(3)}, "status")
	_ = unstructured.SetNestedSlice(o.Object, []interface{}{"keep-me"}, "metadata", "finalizers")
	_ = unstructured.SetNestedSlice(o.Object, []interface{}{
		map[string]interface{}{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web", "uid": "abc"},
	}, "metadata", "ownerReferences")
	_ = unstructured.SetNestedField(o.Object, "12345", "metadata", "resourceVersion")
	_ = unstructured.SetNestedField(o.Object, "8f2c", "metadata", "uid")
	_ = unstructured.SetNestedSlice(o.Object, []interface{}{
		map[string]interface{}{"manager": "kubectl"},
	}, "metadata", "managedFields")
	_ = unstructured.SetNestedMap(o.Object, map[string]interface{}{
		"kubectl.kubernetes.io/last-applied-configuration": "{}",
		"meta.helm.sh/release-name":                        "shop-web",
	}, "metadata", "annotations")

	got, notes := Sanitize(&o)

	for _, path := range [][]string{
		{"status"},
		{"metadata", "resourceVersion"},
		{"metadata", "uid"},
		{"metadata", "managedFields"},
		{"metadata", "finalizers"},
		{"metadata", "ownerReferences"},
		{"metadata", "annotations"},
	} {
		if _, ok, _ := unstructured.NestedFieldNoCopy(got.Object, path...); ok {
			t.Errorf("%v survived sanitizing", path)
		}
	}

	if _, ok, _ := unstructured.NestedInt64(got.Object, "spec", "replicas"); !ok {
		t.Error("spec.replicas was dropped, want it kept")
	}
	if len(notes) != 2 {
		t.Errorf("notes = %v, want one for the finalizers and one for the owner references", notes)
	}

	if len(o.GetFinalizers()) != 1 || o.GetOwnerReferences() == nil {
		t.Error("the input object was mutated, want a deep copy")
	}
}

func TestSanitizeDropsServiceAllocations(t *testing.T) {
	o := *obj("v1", "Service", sandboxNS, "web")
	_ = unstructured.SetNestedMap(o.Object, map[string]interface{}{
		"clusterIP": "10.0.0.7",
		"ports": []interface{}{
			map[string]interface{}{"name": "http", "port": int64(80), "nodePort": int64(30080)},
			map[string]interface{}{"name": "metrics", "port": int64(9090)},
		},
	}, "spec")

	got, notes := Sanitize(&o)

	if _, ok, _ := unstructured.NestedString(got.Object, "spec", "clusterIP"); ok {
		t.Error("clusterIP survived sanitizing")
	}
	ports, _, _ := unstructured.NestedSlice(got.Object, "spec", "ports")
	first, _ := ports[0].(map[string]interface{})
	if _, ok := first["nodePort"]; ok {
		t.Error("nodePort survived sanitizing")
	}
	if first["port"] != int64(80) {
		t.Errorf("port = %v, want 80 to be kept", first["port"])
	}
	if len(notes) != 2 {
		t.Errorf("notes = %v, want one for the addresses and one for the node ports", notes)
	}
}

func TestSanitizeDropsBoundVolume(t *testing.T) {
	o := *obj("v1", "PersistentVolumeClaim", sandboxNS, "data")
	_ = unstructured.SetNestedMap(o.Object, map[string]interface{}{"volumeName": "pvc-1234"}, "spec")

	got, notes := Sanitize(&o)

	if _, ok, _ := unstructured.NestedString(got.Object, "spec", "volumeName"); ok {
		t.Error("volumeName survived sanitizing")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "volumeName") {
		t.Errorf("notes = %v, want one naming the dropped volume", notes)
	}
}

func TestOrderFollowsHelmInstallOrder(t *testing.T) {
	objs := []unstructured.Unstructured{
		*obj("networking.k8s.io/v1", "Ingress", sandboxNS, "web"),
		*obj("apps/v1", "Deployment", sandboxNS, "web"),
		*obj("v1", "Service", sandboxNS, "web"),
		*obj("v1", "ConfigMap", sandboxNS, "settings"),
		*obj("example.com/v1", "Widget", sandboxNS, "unknown-kind"),
	}

	got := Order(objs)

	want := []string{"ConfigMap", "Service", "Deployment", "Ingress", "Widget"}
	for i, kind := range want {
		if got[i].GetKind() != kind {
			t.Fatalf("position %d = %s, want %s (order = %v)", i, got[i].GetKind(), kind, kindNames(got))
		}
	}
}

func TestOrderIsStableWithinAKind(t *testing.T) {
	objs := []unstructured.Unstructured{
		*obj("v1", "ConfigMap", sandboxNS, "b"),
		*obj("v1", "ConfigMap", sandboxNS, "a"),
	}

	got := Order(objs)

	if got[0].GetName() != "b" || got[1].GetName() != "a" {
		t.Fatalf("order = %v, want the input order kept within a kind", names(got))
	}
}

func kindNames(objs []unstructured.Unstructured) []string {
	out := make([]string, 0, len(objs))
	for i := range objs {
		out = append(out, objs[i].GetKind())
	}
	return out
}

func names(objs []unstructured.Unstructured) []string {
	out := make([]string, 0, len(objs))
	for i := range objs {
		out = append(out, objs[i].GetName())
	}
	return out
}
