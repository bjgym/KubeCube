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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ChangeSetLabel marks the ConfigMaps a sandbox records its applies in, so a
// sandbox's history is findable without a second store to keep in step.
const ChangeSetLabel = "kubecube.io/sandbox-change-set"

// changeSetDataKey is where the manifests live in the ConfigMap.
const changeSetDataKey = "manifests"

// ChangeSet is one recorded apply.
type ChangeSet struct {
	// Name is the ConfigMap holding it.
	Name string

	// Digest is the hash of the manifests. A change request carries it so that
	// the person reviewing a change approves exactly what was validated rather
	// than whatever the ConfigMap holds later.
	Digest string
}

// Record stores a change set and returns it, numbered after the sets the
// sandbox already holds.
//
// It is what makes the sandbox iterable: a session applies, fails, applies
// again, and can always return to a set it knows worked. The store is the
// sandbox's own namespace, so it disappears with the sandbox and never needs
// pruning.
func (a Applier) Record(ctx context.Context, objs []unstructured.Unstructured) (ChangeSet, error) {
	if violations := a.Policy.Check(objs); len(violations) > 0 {
		return ChangeSet{}, &RefusedError{Violations: violations}
	}

	manifests, digest, err := encodeChangeSet(objs)
	if err != nil {
		return ChangeSet{}, err
	}

	name, err := a.nextChangeSetName(ctx)
	if err != nil {
		return ChangeSet{}, err
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: a.Policy.Namespace,
			Labels:    map[string]string{ChangeSetLabel: "true"},
		},
		Data: map[string]string{
			changeSetDataKey: manifests,
			"digest":         digest,
		},
	}

	if err := a.Client.Create(ctx, cm); err != nil {
		return ChangeSet{}, fmt.Errorf("recording change set %s: %w", name, err)
	}

	return ChangeSet{Name: name, Digest: digest}, nil
}

// Load reads a recorded change set back.
func (a Applier) Load(ctx context.Context, name string) ([]unstructured.Unstructured, error) {
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: a.Policy.Namespace, Name: name}

	if err := a.Client.Get(ctx, key, cm); err != nil {
		return nil, fmt.Errorf("reading change set %s: %w", name, err)
	}

	return decodeChangeSet(cm.Data[changeSetDataKey])
}

// Rollback applies a recorded change set again.
func (a Applier) Rollback(ctx context.Context, name string) error {
	objs, err := a.Load(ctx, name)
	if err != nil {
		return err
	}

	return a.Apply(ctx, objs)
}

func (a Applier) nextChangeSetName(ctx context.Context) (string, error) {
	sets := &corev1.ConfigMapList{}
	options := []client.ListOption{
		client.InNamespace(a.Policy.Namespace),
		client.MatchingLabels{ChangeSetLabel: "true"},
	}

	if err := a.Client.List(ctx, sets, options...); err != nil {
		return "", fmt.Errorf("listing the sandbox's change sets: %w", err)
	}

	highest := 0
	for i := range sets.Items {
		if n, ok := changeSetNumber(sets.Items[i].Name); ok && n > highest {
			highest = n
		}
	}

	return fmt.Sprintf("change-%04d", highest+1), nil
}

// changeSetNumber reads the number out of a change set's name, and reports
// false for a name this package did not write: a ConfigMap that merely carries
// the label must not decide the next number.
func changeSetNumber(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, "change-")
	if !ok {
		return 0, false
	}

	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return n, true
}

// encodeChangeSet turns a set into the bytes stored for it and the digest that
// names them.
//
// The encoding is JSON rather than YAML because it round-trips an object
// exactly: a manifest that went through a YAML encoder and came back is a
// different document in the places that matter, and the digest is meant to
// identify what was validated.
func encodeChangeSet(objs []unstructured.Unstructured) (string, string, error) {
	documents := make([]map[string]interface{}, 0, len(objs))
	for i := range objs {
		documents = append(documents, objs[i].Object)
	}

	encoded, err := json.Marshal(documents)
	if err != nil {
		return "", "", fmt.Errorf("encoding the change set: %w", err)
	}

	sum := sha256.Sum256(encoded)
	return string(encoded), "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeChangeSet(encoded string) ([]unstructured.Unstructured, error) {
	if encoded == "" {
		return nil, fmt.Errorf("the change set holds no manifests")
	}

	var documents []map[string]interface{}
	if err := json.Unmarshal([]byte(encoded), &documents); err != nil {
		return nil, fmt.Errorf("decoding the change set: %w", err)
	}

	objs := make([]unstructured.Unstructured, 0, len(documents))
	for _, document := range documents {
		objs = append(objs, unstructured.Unstructured{Object: document})
	}

	return objs, nil
}
