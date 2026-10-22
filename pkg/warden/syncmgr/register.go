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

package syncmgr

import (
	"context"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	tenantv1 "github.com/kubecube-io/kubecube/pkg/apis/tenant/v1"
	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

const (
	Skip   = "skip"
	Create = "create"
	Update = "update"
	Delete = "delete"

	// pivot resource version key for compare with local resource
	pivotResourceVersion = "pivotResourceVersion"
)

// unknownResourceVersion stands for a local copy whose recorded pivot version
// cannot be read. It is lower than every real resource version, so the next
// comparison rewrites the copy once and repairs the annotation with it.
const unknownResourceVersion = -1

/*
1. native resource should not be affected?
2. what if sync resource changed manually in member cluster? use webhook?
3. update operation is valid?
4. how to record log?
5. use reflect of not to copy interface value?
*/
// setupCtrlWithManager add reconcile func for each sync resource
// resync reference to https://github.com/cloudnativeto/sig-kubernetes/issues/11
func (s *SyncManager) SetupCtrlWithManager(resource client.Object, objFunc GenericObjFunc) error {
	// the pivot client is served by the manager's cache, the local one is a
	// direct client: the local cluster's own controllers own those writes
	pivotClient := s.Manager.GetClient()
	localClient := s.LocalClient

	return ctrl.NewControllerManagedBy(s).
		For(resource).
		WithEventFilter(eventPredicate()).
		Complete(reconcile.Func(func(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
			return reconcileSyncedObject(ctx, pivotClient, localClient, req, resource, objFunc)
		}))
}

// reconcileSyncedObject mirrors one pivot object into the local cluster, or
// deletes the local copy once the pivot's object is gone.
//
// The clients are parameters rather than fields of SyncManager so that the rules
// below can be driven by a test without a manager, an informer cache or an API
// server. Every defect this file has had lived in them, and each one left an
// object in a state no code path of its own could leave: a copy whose recorded
// version could not be parsed, and a copy whose annotation was empty.
func reconcileSyncedObject(ctx context.Context, pivotClient, localClient client.Client, req reconcile.Request, resource client.Object, objFunc GenericObjFunc) (reconcile.Result, error) {
	var (
		action   = Skip
		err      error
		pivotObj = resource
		localObj = resource
	)

	// record sync log
	defer func() {
		clog.Info("sync: %s %v, name: %v, namespace: %v, err: %v", action, pivotObj.GetObjectKind().GroupVersionKind().Kind, pivotObj.GetName(), pivotObj.GetNamespace(), err)
	}()

	getLocalObj := func() error {

		localObj, err = objFunc(pivotObj)
		if err != nil {
			return err
		}

		trimObjMeta(pivotObj)

		err = localClient.Get(ctx, req.NamespacedName, localObj)
		return err
	}

	// delete
	deleteObjFunc := func() (reconcile.Result, error) {
		err = getLocalObj()
		if err != nil {
			if errors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return reconcile.Result{}, err
		}
		var gvk schema.GroupVersionKind
		gvk, err = apiutil.GVKForObject(localObj, scheme)
		if err != nil {
			return ctrl.Result{}, err
		}
		groupKind := gvk.GroupKind()
		if groupKind.Group == tenantv1.GroupVersion.Group {
			if groupKind.Kind == "Tenant" || groupKind.Kind == "Project" {
				err = updateSpecialObjForDelete(ctx, localClient, req, localObj, objFunc)
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}
		action = Delete
		err = localClient.Delete(ctx, localObj, &client.DeleteOptions{})
		if err != nil && errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if err = pivotClient.Get(ctx, req.NamespacedName, pivotObj); err != nil {
		// If the object is a tenant or project, add an annotation to inform the webhook to allow it to be deleted
		// delete: when object is not exist in pivot cluster
		if errors.IsNotFound(err) {
			return deleteObjFunc()
		}
		return reconcile.Result{}, err
	}

	err = getLocalObj()
	if err != nil {
		if errors.IsNotFound(err) {
			// create: when object is not exist in local cluster
			action = Create
			err = localClient.Create(ctx, pivotObj, &client.CreateOptions{})
			if err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	//If it is the same resource, the managed resource must be created first than the local resource.
	//Based on this, if the management and control creation time is later than the local creation time, it is a new resource
	//Warning, this relies on the local clock, which can cause problems when the clock is wrong or when the clock goes backwards

	pivotCreateTimestamp := pivotObj.GetCreationTimestamp()
	localCreateTimestamp := localObj.GetCreationTimestamp()
	if pivotCreateTimestamp.UnixNano() > localCreateTimestamp.UnixNano() {
		result, err := deleteObjFunc()
		if err != nil {
			return result, err
		}
		action = Create
		err = localClient.Create(ctx, pivotObj, &client.CreateOptions{})
		if err != nil {
			return reconcile.Result{Requeue: true}, err
		}
		return reconcile.Result{}, nil
	}

	// trimObjMeta recorded the pivot's version a few lines up in getLocalObj, so
	// a value that does not parse here is a defect in this package, not an
	// input. Name the object and the value: the bare strconv error is what made
	// the live project look merely skipped while it was frozen for six days.
	pivotVersion := pivotObj.GetAnnotations()[pivotResourceVersion]
	pivotRsVersion, err := strconv.Atoi(pivotVersion)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("the %s annotation recorded for %s %q from the pivot is not an integer (%q): %w",
			pivotResourceVersion, pivotObj.GetObjectKind().GroupVersionKind().Kind, pivotObj.GetName(), pivotVersion, err)
	}

	// The local copy can carry a value warden did not write: an older build
	// recorded an empty one (see trimObjMeta), and an operator or a repair
	// script can annotate the copy by hand. Returning here froze the copy and
	// every later resync failed the same way, so it never converged again.
	// Treat the recorded version as unknown and let the comparison below
	// rewrite the copy, annotation included, from the pivot: the pivot is the
	// source of truth and this annotation is only warden's own bookkeeping.
	localVersion := localObj.GetAnnotations()[pivotResourceVersion]
	localRsVersion, err := strconv.Atoi(localVersion)
	if err != nil {
		clog.Warn("sync: %s %q records the unreadable %s annotation %q, rewriting the copy from the pivot",
			localObj.GetObjectKind().GroupVersionKind().Kind, localObj.GetName(), pivotResourceVersion, localVersion)
		localRsVersion = unknownResourceVersion
	}

	// update: when pivot resource version bigger than local resource version
	if pivotRsVersion > localRsVersion {
		action = Update
		pivotObj.SetResourceVersion(localObj.GetResourceVersion())
		pivotObj.SetUID(localObj.GetUID())
		err = localClient.Update(ctx, pivotObj, &client.UpdateOptions{})
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

// trimObjMeta trims the read-only metadata of an object on its way to the local
// cluster and records the pivot's resource version, which is the only thing the
// next reconcile compares: the version is read before it is cleared below.
func trimObjMeta(obj client.Object) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		// A pivot object that was not found is never decoded into the reused
		// prototype, so this is reached with no annotation map at all on the delete
		// path. Assigning into a nil map panics, and a panic here restarts warden.
		annotations = make(map[string]string)
	}

	// An empty version is never recorded. The recreate path below calls this twice
	// in one reconcile (getLocalObj, then deleteObjFunc), and the second call would
	// otherwise record "" for an object whose version had just been cleared. The
	// local copy then carried an annotation strconv.Atoi cannot parse, and every
	// later resync failed on it: the object was frozen for good.
	if version := obj.GetResourceVersion(); version != "" {
		annotations[pivotResourceVersion] = version
	}

	if _, ok := annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
	}

	obj.SetAnnotations(annotations)
	obj.SetResourceVersion("")
}

// isSyncResource determined if sync that event
func isSyncResource(obj client.Object) bool {
	// resource inherited by hnc do not need sync
	if _, ok := obj.GetLabels()[constants.HncInherited]; ok {
		return false
	}

	if v, ok := obj.GetAnnotations()[constants.SyncAnnotation]; ok {
		b, err := strconv.ParseBool(v)
		if b && err == nil {
			return true
		}
		if err != nil {
			clog.Error("value format of annotation %v failed: %v, got value: %v%", constants.SyncAnnotation, err, v)
		}
	}

	return false
}

// eventPredicate do event filter for reconcile
func eventPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(event event.CreateEvent) bool {
			return isSyncResource(event.Object)
		},
		UpdateFunc: func(updateEvent event.UpdateEvent) bool {
			return isSyncResource(updateEvent.ObjectNew)
		},
		DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
			return isSyncResource(deleteEvent.Object)
		},
		GenericFunc: func(genericEvent event.GenericEvent) bool {
			return false
		},
	}
}

func updateSpecialObjForDelete(ctx context.Context, localClient client.Client, req reconcile.Request, obj client.Object, objFunc GenericObjFunc) error {
	newObj, err := objFunc(obj)
	if err != nil {
		return err
	}
	err = localClient.Get(ctx, req.NamespacedName, newObj)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	annotation := newObj.GetAnnotations()
	if annotation == nil {
		annotation = make(map[string]string)
	}
	annotation[constants.ForceDeleteAnnotation] = "true"
	newObj.SetAnnotations(annotation)
	return localClient.Update(ctx, newObj)
}
