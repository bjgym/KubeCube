/*
Copyright 2022 KubeCube Authors

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

package project

import (
	"context"
	"errors"
	"fmt"

	v1 "k8s.io/api/core/v1"

	tenantv1 "github.com/kubecube-io/kubecube/pkg/apis/tenant/v1"
	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/multicluster"
	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
	"github.com/kubecube-io/kubecube/pkg/utils/domain"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var notFoundLabelErr = errors.New("can not find .metadata.labels.kubecube.io/tenant label")

func (r *Validator) ValidateCreate(project *tenantv1.Project) error {
	tenantName := project.Labels[constants.TenantLabel]
	if tenantName == "" {
		clog.Info(notFoundLabelErr.Error())
		return notFoundLabelErr
	}

	ctx := context.Background()
	tenant := tenantv1.Tenant{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: tenantName}, &tenant); err != nil {
		clog.Info("The tenant %s is not exist", tenantName)
		return fmt.Errorf("the tenant is not exist")
	}

	if err := domain.ValidatorDomainSuffix(project.Spec.IngressDomainSuffix); err != nil {
		return err
	}

	clog.Debug("Create validate success, project info: %v", project)
	return nil
}

func (r *Validator) ValidateUpdate(_ *tenantv1.Project, currentProject *tenantv1.Project) error {

	tenantName := currentProject.Labels[constants.TenantLabel]
	if tenantName == "" {
		clog.Info(notFoundLabelErr.Error())
		return notFoundLabelErr
	}

	ctx := context.Background()
	tenant := tenantv1.Tenant{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: tenantName}, &tenant); err != nil {
		clog.Info("The tenant %s is not exist", tenantName)
		return fmt.Errorf("the tenant is not exist")
	}

	if err := domain.ValidatorDomainSuffix(currentProject.Spec.IngressDomainSuffix); err != nil {
		return err
	}

	clog.Debug("Update validate success, project info: %v", currentProject)

	return nil
}

// DeleteWarnings reports what deleting a project takes with it.
//
// As with a tenant, it no longer refuses the deletion: warden deletes a
// project's spaces once the resource is gone, so refusing would deadlock a
// tenant's cascade, which deletes projects while their spaces still exist. The
// warning keeps the information the refusal used to carry.
func (r *Validator) DeleteWarnings(project *tenantv1.Project) (admission.Warnings, error) {
	// check the namespace we take over has been already deleted
	ctx := context.Background()
	clusters := multicluster.Interface().FuzzyCopy()

	// a project's spaces are what goes with it. Its own namespace is not a
	// space, so it does not block its own project from being deleted.
	lbSelector := ownership.SpaceSelector(project.Name)

	for _, cluster := range clusters {
		namespaceList := v1.NamespaceList{}
		if err := cluster.Client.Cache().List(ctx, &namespaceList, &client.ListOptions{LabelSelector: lbSelector}); err != nil {
			clog.Error("Can not list namespaces under this project: %v", err.Error())
			return nil, fmt.Errorf("can not list namespaces under this project")
		}
		if len(namespaceList.Items) > 0 {
			return admission.Warnings{
				fmt.Sprintf("deleting project %s also deletes its namespaces and everything in them", project.Name),
			}, nil
		}
	}
	return nil, nil
}
