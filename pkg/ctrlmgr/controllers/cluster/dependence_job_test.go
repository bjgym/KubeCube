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

package controllers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/utils/env"
)

const testDependenceJobName = "install-dependence"

// TestMain sets the package logger, which newReconciler sets in production and
// createResource reads. Without it every test that reaches createResource
// panics on a nil logger.
func TestMain(m *testing.M) {
	log = clog.WithName("cluster-test")
	os.Exit(m.Run())
}

func dependenceJobKey() types.NamespacedName {
	return types.NamespacedName{Name: testDependenceJobName, Namespace: env.CubeNamespace()}
}

// dependenceJob builds a job that started the given time ago.
func dependenceJob(conditions []batchv1.JobCondition, startedAgo time.Duration) *batchv1.Job {
	started := metav1.NewTime(time.Now().Add(-startedAgo))
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:              testDependenceJobName,
			Namespace:         env.CubeNamespace(),
			CreationTimestamp: started,
		},
		Status: batchv1.JobStatus{StartTime: &started, Conditions: conditions},
	}
}

func jobCondition(t batchv1.JobConditionType, s corev1.ConditionStatus, reason string) batchv1.JobCondition {
	return batchv1.JobCondition{Type: t, Status: s, Reason: reason}
}

func newFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot build the scheme: %v", err)
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestReadDependenceJob(t *testing.T) {
	tests := []struct {
		name        string
		job         *batchv1.Job
		noStartTime bool
		wantState   dependenceJobState
		wantReason  string
	}{
		{
			name:      "no job yet",
			wantState: dependenceJobAbsent,
		},
		{
			name:      "running and nothing reported",
			job:       dependenceJob(nil, time.Minute),
			wantState: dependenceJobRunning,
		},
		{
			// a condition that is not true is not a result, and reading it as one
			// would let the platform walk past a bootstrap that never ran
			name: "a false condition is not a result",
			job: dependenceJob([]batchv1.JobCondition{
				jobCondition(batchv1.JobComplete, corev1.ConditionFalse, ""),
			}, time.Minute),
			wantState: dependenceJobRunning,
		},
		{
			name: "completed",
			job: dependenceJob([]batchv1.JobCondition{
				jobCondition(batchv1.JobComplete, corev1.ConditionTrue, ""),
			}, time.Minute),
			wantState: dependenceJobSucceeded,
		},
		{
			name: "failed, with the reason it gave",
			job: dependenceJob([]batchv1.JobCondition{
				jobCondition(batchv1.JobFailed, corev1.ConditionTrue, "BackoffLimitExceeded"),
			}, time.Minute),
			wantState:  dependenceJobFailed,
			wantReason: "BackoffLimitExceeded",
		},
		{
			// without this the reconcile would requeue forever on a job that
			// never reports, which is worse than the timeout it replaced
			name:       "still running past its bound is a failure",
			job:        dependenceJob(nil, dependenceJobTimeout+time.Minute),
			wantState:  dependenceJobFailed,
			wantReason: "still running after",
		},
		{
			name:        "never started, but created past its bound",
			job:         dependenceJob(nil, dependenceJobTimeout+time.Minute),
			noStartTime: true,
			wantState:   dependenceJobFailed,
			wantReason:  "still running after",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.job != nil {
				if tt.noStartTime {
					tt.job.Status.StartTime = nil
				}
				objects = append(objects, tt.job)
			}

			state, reason, err := readDependenceJob(context.Background(), newFakeClient(t, objects...), dependenceJobKey())
			if err != nil {
				t.Fatalf("readDependenceJob() error = %v", err)
			}
			if state != tt.wantState {
				t.Errorf("readDependenceJob() state = %v, want %v", state, tt.wantState)
			}
			if tt.wantReason != "" && !strings.Contains(reason, tt.wantReason) {
				t.Errorf("readDependenceJob() reason = %q, want it to contain %q", reason, tt.wantReason)
			}
		})
	}
}

func TestEnsureDependenceJob(t *testing.T) {
	job := func() *batchv1.Job {
		j := makePrevJob()
		return j
	}

	t.Run("refuses to create a job with no image", func(t *testing.T) {
		t.Setenv("DEPENDENCE_JOB_IMAGE", "")

		cli := newFakeClient(t)
		if _, err := ensureDependenceJob(context.Background(), cli, "member-1"); err == nil {
			t.Fatal("ensureDependenceJob() error = nil, want an error when the image is unset")
		}

		created := batchv1.Job{}
		if err := cli.Get(context.Background(), dependenceJobKey(), &created); err == nil {
			t.Error("ensureDependenceJob() created a job even though the image was unset")
		}
	})

	t.Run("creates the job once and reports it as unfinished", func(t *testing.T) {
		t.Setenv("DEPENDENCE_JOB_IMAGE", "example.com/kubecube/warden-dependence:v1")

		cli := newFakeClient(t)
		done, err := ensureDependenceJob(context.Background(), cli, "member-1")
		if err != nil {
			t.Fatalf("ensureDependenceJob() error = %v", err)
		}
		if done {
			t.Error("ensureDependenceJob() reported done for a job it just created")
		}

		created := batchv1.Job{}
		if err := cli.Get(context.Background(), dependenceJobKey(), &created); err != nil {
			t.Fatalf("the job was not created: %v", err)
		}
		if created.Spec.Template.Spec.Containers[0].Image != "example.com/kubecube/warden-dependence:v1" {
			t.Errorf("the job runs image %q, want the configured one", created.Spec.Template.Spec.Containers[0].Image)
		}
	})

	t.Run("does not touch a job that already completed", func(t *testing.T) {
		t.Setenv("DEPENDENCE_JOB_IMAGE", "example.com/kubecube/warden-dependence:v1")

		completed := job()
		completed.Status.Conditions = []batchv1.JobCondition{
			jobCondition(batchv1.JobComplete, corev1.ConditionTrue, ""),
		}
		cli := newFakeClient(t, completed)

		done, err := ensureDependenceJob(context.Background(), cli, "member-1")
		if err != nil {
			t.Fatalf("ensureDependenceJob() error = %v", err)
		}
		if !done {
			t.Error("ensureDependenceJob() reported a completed job as unfinished")
		}
	})
}
