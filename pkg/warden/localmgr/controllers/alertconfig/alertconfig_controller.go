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

// Package alertconfig materialises a project's alerting configuration into the
// spaces that belong to it.
//
// It exists because prometheus-operator scopes an AlertmanagerConfig to its own
// namespace, and the console writes a project's alert policy into the project
// namespace: without a copy in each space, that policy silently stops matching
// the spaces' alerts. It replaces what HNC did for these two types, and it is
// deliberately narrower than HNC was — a project's AlertmanagerConfigs and
// exactly the Secrets they reference, rather than every Secret written in the
// project namespace.
package alertconfig

import (
	"context"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

const (
	alertmanagerConfigGroup   = "monitoring.coreos.com"
	alertmanagerConfigVersion = "v1alpha1"
	alertmanagerConfigKind    = "AlertmanagerConfig"

	// resync is a deliberate, bounded poll. Secrets are read straight from the
	// API server instead of being cached, because caching every Secret in the
	// cluster is part of what made HNC heavy. The cost is that a rotated
	// credential, and a Secret that appears after the config referring to it,
	// converge within this window rather than immediately.
	resync = 30 * time.Minute
)

// Reconciler copies a project's alerting configuration into its spaces.
type Reconciler struct {
	client.Client

	// reader reads straight from the API server. The AlertmanagerConfig kind is
	// a CRD that may not be served at all, and Secrets are deliberately not
	// cached, so neither goes through the manager's cache.
	reader client.Reader

	// configsServed is false when the monitoring CRDs are absent, which is the
	// normal case for a deployment without the monitoring addon. A watch on an
	// absent kind fails the controller at start-up, so the watch is conditional
	// and this records the answer.
	configsServed bool
}

// SetupWithManager registers the reconciler. The AlertmanagerConfig watch is
// added only when the cluster serves that kind.
func SetupWithManager(m manager.Manager) error {
	r := &Reconciler{Client: m.GetClient(), reader: m.GetAPIReader()}

	builder := ctrl.NewControllerManagedBy(m).
		Named("alertconfig").
		For(&corev1.Namespace{})

	if served(m.GetRESTMapper()) {
		r.configsServed = true
		builder = builder.Watches(alertmanagerConfigObject(), handler.EnqueueRequestsFromMapFunc(r.configMapFunc))
	}

	return builder.Complete(r)
}

func served(mapper meta.RESTMapper) bool {
	_, err := mapper.RESTMapping(schema.GroupKind{Group: alertmanagerConfigGroup, Kind: alertmanagerConfigKind}, alertmanagerConfigVersion)
	return err == nil
}

func alertmanagerConfigGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   alertmanagerConfigGroup,
		Version: alertmanagerConfigVersion,
		Kind:    alertmanagerConfigKind,
	}
}

func alertmanagerConfigListGVK() schema.GroupVersionKind {
	gvk := alertmanagerConfigGVK()
	gvk.Kind += "List"
	return gvk
}

func alertmanagerConfigObject() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(alertmanagerConfigGVK())
	return obj
}

// configMapFunc maps a changed AlertmanagerConfig to the namespace holding it,
// whose reconcile refreshes every space that project has.
func (r *Reconciler) configMapFunc(_ context.Context, obj client.Object) []reconcile.Request {
	if obj.GetNamespace() == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: obj.GetNamespace()}}}
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !r.configsServed {
		return ctrl.Result{}, nil
	}

	ns := &corev1.Namespace{}
	if err := r.Get(ctx, req.NamespacedName, ns); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// a namespace on its way out needs no copies, and writing into it would be
	// rejected
	if !ns.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	project, owned := ownership.ProjectOf(ns)
	if !owned {
		return ctrl.Result{}, nil
	}

	// an object in the project namespace changed: refresh every space under it.
	// Otherwise this is a space, and it is brought in line with its project.
	if level, _ := ownership.LevelOf(ns); level == ownership.LevelProject {
		return r.materializeProject(ctx, project)
	}

	source := r.projectNamespace(ctx, project)
	if source == "" || source == ns.Name {
		return ctrl.Result{}, nil
	}

	return r.materialize(ctx, source, ns.Name)
}

func (r *Reconciler) materializeProject(ctx context.Context, project string) (ctrl.Result, error) {
	source := r.projectNamespace(ctx, project)
	if source == "" {
		return ctrl.Result{}, nil
	}

	spaces := &corev1.NamespaceList{}
	if err := r.List(ctx, spaces, client.MatchingLabelsSelector{Selector: ownership.SpaceSelector(project)}); err != nil {
		return ctrl.Result{}, err
	}

	for i := range spaces.Items {
		if !spaces.Items[i].DeletionTimestamp.IsZero() {
			continue
		}
		if _, err := r.materialize(ctx, source, spaces.Items[i].Name); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// projectNamespace finds the namespace a project owns. The project's own
// namespace is the one at the project level, which avoids consulting the name
// convention.
func (r *Reconciler) projectNamespace(ctx context.Context, project string) string {
	owned := &corev1.NamespaceList{}
	err := r.List(ctx, owned, client.MatchingLabelsSelector{Selector: ownership.Selector(ownership.KindProject, project)})
	if err != nil {
		clog.Warn("alertconfig: cannot list the namespaces of project %s: %v", project, err)
		return ""
	}

	for i := range owned.Items {
		if level, ok := ownership.LevelOf(&owned.Items[i]); ok && level == ownership.LevelProject {
			return owned.Items[i].Name
		}
	}

	return ""
}

// materialize brings one space in line with the project it belongs to.
func (r *Reconciler) materialize(ctx context.Context, source, space string) (ctrl.Result, error) {
	configs, err := r.listConfigs(ctx, source)
	if err != nil {
		return ctrl.Result{}, err
	}

	desiredConfigs := map[string]bool{}
	desiredSecrets := map[string]bool{}

	for i := range configs {
		config := &configs[i]
		desiredConfigs[config.GetName()] = true

		for _, name := range secretReferences(config.Object["spec"]) {
			desiredSecrets[name] = true
		}

		if err := r.copyConfig(ctx, config, source, space); err != nil {
			return ctrl.Result{}, err
		}
	}

	for _, name := range sortedNames(desiredSecrets) {
		present, err := r.copySecret(ctx, source, space, name)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !present {
			clog.Warn("alertconfig: project namespace %s has no secret %s, so the config referring to it cannot work in space %s", source, name, space)
		}
	}

	if err := r.deleteStale(ctx, source, space, desiredConfigs, desiredSecrets); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: resync}, nil
}

func (r *Reconciler) listConfigs(ctx context.Context, namespace string) ([]unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(alertmanagerConfigListGVK())

	if err := r.reader.List(ctx, list, client.InNamespace(namespace)); err != nil {
		// the kind can disappear under a running controller when monitoring is
		// uninstalled
		if meta.IsNoMatchError(err) || errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	return list.Items, nil
}

func (r *Reconciler) copyConfig(ctx context.Context, config *unstructured.Unstructured, source, space string) error {
	desired := configCopy(config, source, space)

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(config.GroupVersionKind())

	err := r.reader.Get(ctx, types.NamespacedName{Namespace: space, Name: config.GetName()}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if !configChanged(existing, desired) {
		return nil
	}

	desired.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, desired)
}

// configCopy builds a space's copy of a project's AlertmanagerConfig.
//
// The labels are carried over unchanged on purpose: the alertmanager selects
// the configs it merges by kubecube.io/owner, so a copy that dropped it would
// be ignored.
func configCopy(config *unstructured.Unstructured, source, space string) *unstructured.Unstructured {
	copied := &unstructured.Unstructured{}
	copied.SetGroupVersionKind(config.GroupVersionKind())
	copied.SetName(config.GetName())
	copied.SetNamespace(space)
	copied.SetLabels(materializedFrom(config.GetLabels(), source))
	copied.SetAnnotations(config.GetAnnotations())

	if spec, ok := config.Object["spec"]; ok {
		// the spec came out of the API server as JSON, so it is already made of
		// the types SetNestedField accepts
		_ = unstructured.SetNestedField(copied.Object, spec, "spec")
	}

	return copied
}

func configChanged(existing, desired *unstructured.Unstructured) bool {
	if !reflect.DeepEqual(existing.Object["spec"], desired.Object["spec"]) {
		return true
	}
	return !reflect.DeepEqual(existing.GetLabels(), desired.GetLabels())
}

// copySecret copies one Secret out of the project namespace into the space, and
// reports whether the project had it.
func (r *Reconciler) copySecret(ctx context.Context, source, space, name string) (bool, error) {
	origin := &corev1.Secret{}
	err := r.reader.Get(ctx, types.NamespacedName{Namespace: source, Name: name}, origin)
	if errors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   space,
			Labels:      materializedFrom(origin.GetLabels(), source),
			Annotations: origin.GetAnnotations(),
		},
		Data: origin.Data,
		Type: origin.Type,
	}

	existing := &corev1.Secret{}
	err = r.reader.Get(ctx, types.NamespacedName{Namespace: space, Name: name}, existing)
	if errors.IsNotFound(err) {
		return true, r.Create(ctx, desired)
	}
	if err != nil {
		return true, err
	}

	if !secretChanged(existing, desired) {
		return true, nil
	}

	desired.ResourceVersion = existing.ResourceVersion
	return true, r.Update(ctx, desired)
}

func secretChanged(existing, desired *corev1.Secret) bool {
	return existing.Type != desired.Type ||
		!reflect.DeepEqual(existing.Data, desired.Data) ||
		!reflect.DeepEqual(existing.Labels, desired.Labels)
}

// materializedFrom marks a copy with the namespace it came from, which is how
// it is found again for update and deletion.
func materializedFrom(labels map[string]string, source string) map[string]string {
	marked := make(map[string]string, len(labels)+1)
	for key, value := range labels {
		marked[key] = value
	}
	marked[constants.MaterializedFromLabel] = source
	return marked
}

// deleteStale removes the copies whose source is gone, so that deleting an
// AlertmanagerConfig in the project stops its alerting in the spaces.
func (r *Reconciler) deleteStale(ctx context.Context, source, space string, configs, secrets map[string]bool) error {
	marked := client.MatchingLabels{constants.MaterializedFromLabel: source}

	staleConfigs := &unstructured.UnstructuredList{}
	staleConfigs.SetGroupVersionKind(alertmanagerConfigListGVK())
	if err := r.reader.List(ctx, staleConfigs, client.InNamespace(space), marked); err != nil {
		if !meta.IsNoMatchError(err) && !errors.IsNotFound(err) {
			return err
		}
	}
	for i := range staleConfigs.Items {
		if configs[staleConfigs.Items[i].GetName()] {
			continue
		}
		if err := r.Delete(ctx, &staleConfigs.Items[i]); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	staleSecrets := &corev1.SecretList{}
	if err := r.reader.List(ctx, staleSecrets, client.InNamespace(space), marked); err != nil {
		return err
	}
	for i := range staleSecrets.Items {
		if secrets[staleSecrets.Items[i].Name] {
			continue
		}
		if err := r.Delete(ctx, &staleSecrets.Items[i]); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	return nil
}
