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

package sso

import (
	"context"
	"net/url"

	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubecube-io/kubecube/pkg/authentication"
	"github.com/kubecube-io/kubecube/pkg/clients"
	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
	"github.com/kubecube-io/kubecube/pkg/utils/env"
	"github.com/kubecube-io/kubecube/pkg/warden/localmgr/controllers/hotplug"
)

// configMapName is the name of both the ConfigMap holding the public part of
// every identity provider config (enabled, clientId) and the companion Secret
// holding the private part (client secret and the two endpoint templates).
const configMapName = "kubecube-auth-config"

// getConfig assembles the sso provider config for the provider named name.
// An empty SsoConfig (IsEnable false) is returned whenever the provider is
// disabled or either half of its configuration is missing.
func getConfig(name string) authentication.SsoConfig {
	var ssoConfig authentication.SsoConfig

	kClient := clients.Interface().Kubernetes(constants.LocalCluster).Cache()
	if kClient == nil {
		clog.Error("get pivot cluster client is nil")
		return ssoConfig
	}
	cm := &v1.ConfigMap{}
	err := kClient.Get(context.Background(), client.ObjectKey{Name: configMapName, Namespace: env.CubeNamespace()}, cm)
	if err != nil {
		clog.Error("get configmap from K8s err: %v", err)
		return ssoConfig
	}

	config := cm.Data[name]
	if config == "" {
		clog.Error("%v config is nil", name)
		return ssoConfig
	}

	configJson, err := hotplug.YamlStringToJson(config)
	if err != nil {
		clog.Error("%v", err.Error())
		return ssoConfig
	}

	if enabled, ok := configJson["enabled"].(bool); !ok || !enabled {
		return ssoConfig
	}
	ssoConfig.IsEnable = true

	if clientId, ok := configJson["clientId"].(string); ok {
		ssoConfig.ClientID = clientId
	}

	secrets := &v1.Secret{}
	err = kClient.Get(context.Background(), client.ObjectKey{Name: configMapName, Namespace: env.CubeNamespace()}, secrets)
	if err != nil {
		clog.Error("get secrets from K8s err: %v", err)
		return ssoConfig
	}

	clientSecret := string(secrets.Data[name])
	if clientSecret == "" {
		clog.Error("%v clientSecret is nil", name)
		return ssoConfig
	}

	decodedStr, err := url.QueryUnescape(clientSecret)
	if err != nil {
		clog.Error("%v", err.Error())
		return ssoConfig
	}
	clientSecretJson, err := hotplug.YamlStringToJson(decodedStr)
	if err != nil {
		clog.Error("%v", err.Error())
		return ssoConfig
	}

	if v, ok := clientSecretJson["client_secret"].(string); ok {
		ssoConfig.ClientSecret = v
	}

	if v, ok := clientSecretJson["tokenUrl"].(string); ok {
		ssoConfig.TokenUrl = v
	}

	if v, ok := clientSecretJson["userUrl"].(string); ok {
		ssoConfig.UserUrl = v
	}

	return ssoConfig
}
