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

package belongs

import (
	v1 "github.com/kubecube-io/kubecube/pkg/apis/user/v1"
	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type JudgementFunc func(user *v1.User, obj runtime.Object) (bool, error)

// not thread safe
var resourcesHandlers = map[schema.GroupVersionResource]JudgementFunc{
	{Version: "v1", Resource: constants.ResourceNamespaces}: namespaceJudgement,
	{Version: "v1", Resource: constants.ResourceNode}:       nodeJudgment,
}

func RegisterDeterminer(gvr schema.GroupVersionResource, fn JudgementFunc) {
	resourcesHandlers[gvr] = fn
}

func GetDeterminer(gvr schema.GroupVersionResource) JudgementFunc {
	return resourcesHandlers[gvr]
}

func nodeJudgment(user *v1.User, obj runtime.Object) (bool, error) {
	if v1.IsPlatformAdmin(user) {
		return true, nil
	}

	meatObj, err := meta.Accessor(obj)
	if err != nil {
		return false, err
	}

	if meatObj.GetLabels() == nil {
		return false, nil
	}

	NodeTenant, ok := meatObj.GetLabels()[constants.LabelNodeTenant]
	if ok {
		if NodeTenant == constants.ValueNodeShare {
			return true, nil
		}
		if v1.BelongsToTenant(user, NodeTenant) {
			return true, nil
		}
	}

	return false, nil
}

func namespaceJudgement(user *v1.User, obj runtime.Object) (bool, error) {
	if v1.IsPlatformAdmin(user) {
		return true, nil
	}

	meatObj, err := meta.Accessor(obj)
	if err != nil {
		return false, err
	}

	// A namespace is reachable through either fact it carries: the tenant it
	// belongs to, or the project that owns it. Both are asked, because a tenant
	// member reaches a project's namespace through their tenant and a project
	// member reaches it through their project, and neither implies the other.
	if tenant, ok := ownership.TenantOf(meatObj); ok && v1.BelongsToTenant(user, tenant) {
		return true, nil
	}

	if project, ok := ownership.ProjectOf(meatObj); ok && v1.BelongsToProject(user, project) {
		return true, nil
	}

	return false, nil
}
