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
	"strconv"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	tenantv1 "github.com/kubecube-io/kubecube/pkg/apis/tenant/v1"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// The objects below are the live ones: a project synced from the pivot, whose
// member copy recorded the pivot's revision 227254 before that annotation
// stopped being a number.
const (
	liveProjectName = "p-verify"
	liveTenantName  = "t-verify"
	liveVersion     = "227254"
)

// liveProject is a Project as the pivot holds it: synced to its members, labelled
// with its tenant, and carrying the resource version the member's copy records.
func liveProject(created time.Time) *tenantv1.Project {
	return &tenantv1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:              liveProjectName,
			ResourceVersion:   liveVersion,
			CreationTimestamp: metav1.NewTime(created),
			Annotations:       map[string]string{constants.SyncAnnotation: "1"},
			Labels:            map[string]string{constants.TenantLabel: liveTenantName},
		},
		Spec: tenantv1.ProjectSpec{
			DisplayName: "verification project",
			Description: "created for the limited-permission verification",
			Namespace:   constants.ProjectNsPrefix + liveProjectName,
		},
	}
}

// liveProjectClients returns one fake client per cluster. The pivot object keeps
// the resource version the member's annotation is supposed to record.
func liveProjectClients(t *testing.T, pivot, local *tenantv1.Project) (client.Client, client.Client) {
	t.Helper()

	pivotClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pivot).Build()
	localClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(local).Build()

	return pivotClient, localClient
}

// reconcileProject drives one sync of the project, with the prototype the
// controller reuses for every reconcile (SetupCtrlWithManager hands it in once).
func reconcileProject(t *testing.T, pivotClient, localClient client.Client, prototype *tenantv1.Project) error {
	t.Helper()

	_, err := reconcileSyncedObject(context.Background(), pivotClient, localClient,
		reconcile.Request{NamespacedName: types.NamespacedName{Name: liveProjectName}},
		prototype, newGenericObj)

	return err
}

func getLocalProject(t *testing.T, localClient client.Client) *tenantv1.Project {
	t.Helper()

	stored := &tenantv1.Project{}
	if err := localClient.Get(context.Background(), types.NamespacedName{Name: liveProjectName}, stored); err != nil {
		t.Fatalf("cannot read the member's copy of the project back: %v", err)
	}

	return stored
}

// The pivot re-creates an object, so its creation timestamp is newer than the
// member's copy and the sync manager deletes and recreates that copy. The
// version it records must be the pivot's: trimObjMeta is called twice in this
// pass (getLocalObj, then deleteObjFunc) and the second call used to record the
// version the first call had just cleared.
func TestRecreateRecordsThePivotVersion(t *testing.T) {
	pivotCreated := time.Date(2026, 10, 16, 3, 53, 43, 0, time.UTC)

	pivot := liveProject(pivotCreated)
	local := liveProject(pivotCreated.Add(-2 * time.Hour))
	local.ResourceVersion = "20"
	local.Annotations[pivotResourceVersion] = "1"

	pivotClient, localClient := liveProjectClients(t, pivot, local)
	prototype := &tenantv1.Project{}

	if err := reconcileProject(t, pivotClient, localClient, prototype); err != nil {
		t.Fatalf("the recreate of the member's copy failed: %v", err)
	}

	version := getLocalProject(t, localClient).GetAnnotations()[pivotResourceVersion]
	if _, err := strconv.Atoi(version); err != nil {
		// Error, not Fatal: the live symptom is exactly this strconv error, and it
		// is the reason the copy never syncs again.
		t.Errorf("the member's copy records %s %q, which strconv.Atoi rejects (%v), so the project is skipped on every resync; want %q",
			pivotResourceVersion, version, err, liveVersion)
	}
	if version != liveVersion {
		t.Errorf("the member's copy records %s %q, want the pivot's revision %q",
			pivotResourceVersion, version, liveVersion)
	}

	// The loop the live cluster is in: the copy the recreate wrote could not be
	// read back on the next resync either.
	if err := reconcileProject(t, pivotClient, localClient, prototype); err != nil {
		t.Fatalf("the resync after the recreate failed: %v", err)
	}
}

// The live member copy: the pivot's revision, but written with the quotes of the
// JSON document it was copied from. A value warden cannot parse is its own
// bookkeeping and must be repaired from the pivot, not skipped forever.
func TestUnreadableLocalVersionIsRepairedFromThePivot(t *testing.T) {
	pivotCreated := time.Date(2026, 10, 16, 3, 53, 43, 0, time.UTC)

	pivot := liveProject(pivotCreated)
	local := liveProject(pivotCreated)
	local.ResourceVersion = "20"
	local.Annotations[pivotResourceVersion] = `"` + liveVersion + `"`

	pivotClient, localClient := liveProjectClients(t, pivot, local)

	if err := reconcileProject(t, pivotClient, localClient, &tenantv1.Project{}); err != nil {
		t.Fatalf("the reconcile of a copy whose recorded version cannot be parsed failed: %v", err)
	}

	stored := getLocalProject(t, localClient)
	if got := stored.GetAnnotations()[pivotResourceVersion]; got != liveVersion {
		t.Errorf("the copy still records %s %q, want the pivot's revision %q",
			pivotResourceVersion, got, liveVersion)
	}
	if stored.ResourceVersion == "20" {
		t.Error("the copy was not rewritten, so its annotation can never be repaired")
	}
}

// The writer itself. The recreate path calls trimObjMeta twice in one reconcile,
// and the second call must keep the version the first one recorded.
func TestTrimObjMetaKeepsTheVersionItJustRecorded(t *testing.T) {
	project := liveProject(time.Date(2026, 10, 16, 3, 53, 43, 0, time.UTC))

	trimObjMeta(project)
	trimObjMeta(project)

	if got := project.GetAnnotations()[pivotResourceVersion]; got != liveVersion {
		t.Errorf("the second trim recorded %s %q, want %q: it read the resource version the first trim cleared",
			pivotResourceVersion, got, liveVersion)
	}
}

// A pivot object that was not found is never decoded into the prototype, so the
// delete path reaches trimObjMeta with no annotation map at all.
func TestTrimObjMetaDoesNotPanicWithoutAnnotations(t *testing.T) {
	project := &tenantv1.Project{ObjectMeta: metav1.ObjectMeta{Name: liveProjectName}}

	trimObjMeta(project)

	if _, ok := project.GetAnnotations()[pivotResourceVersion]; ok {
		t.Errorf("an object with no resource version recorded one: %q", project.GetAnnotations()[pivotResourceVersion])
	}
}
