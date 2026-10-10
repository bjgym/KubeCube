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

package rbac

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// caBundleLine matches the bundle the pre-install job fills in: the text the
// chart ships carries a placeholder there rather than a certificate.
var caBundleLine = regexp.MustCompile(`(?m)^(\s*caBundle:\s*).*$`)

// caBundlePlaceholder is a value the parser accepts. Nothing here reads it.
const caBundlePlaceholder = "cGxhY2Vob2xkZXI="

// localWebhookFiles are the two configurations the local install path applies.
// They exist because a local run has no pre-install job to mint a CA and apply
// them, and they must keep the same boundaries as the chart's — which is what
// this file checks, because they had drifted: their ResourceQuota webhook was
// `failurePolicy: Fail` with no selector, so a local install intercepted every
// ResourceQuota in the cluster.
var localWebhookFiles = []string{
	filepath.Join("..", "..", "..", "deploy", "manifests", "cubeWebhook.yaml"),
	filepath.Join("..", "..", "..", "deploy", "manifests", "wardenWebhook.yaml"),
}

// TestTheLocalWebhookConfigurationsKeepTheirBoundaries runs without a chart, so
// it is the half of this file CI can run.
func TestTheLocalWebhookConfigurationsKeepTheirBoundaries(t *testing.T) {
	for _, path := range localWebhookFiles {
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		configuration := &admissionv1.ValidatingWebhookConfiguration{}
		if err := yaml.Unmarshal(document, configuration); err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		assertWebhookBoundaries(t, filepath.Base(path), configuration)
	}
}

// TestTheChartsWebhookConfigurationsKeepTheirBoundaries reads the ones the
// chart ships, which live as text inside the pre-install job's ConfigMap: they
// are applied by that job, after it has minted the CA their caBundle carries.
func TestTheChartsWebhookConfigurationsKeepTheirBoundaries(t *testing.T) {
	configurations := chartWebhookConfigurations(t)

	for name, configuration := range configurations {
		assertWebhookBoundaries(t, name, configuration)
	}
}

// TestTheLocalAndChartWebhooksAgree is what keeps the local copies from drifting
// again: the same webhook must decide the same way in both.
func TestTheLocalAndChartWebhooksAgree(t *testing.T) {
	chart := chartWebhookConfigurations(t)

	local := map[string]*admissionv1.ValidatingWebhookConfiguration{}
	for _, path := range localWebhookFiles {
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		configuration := &admissionv1.ValidatingWebhookConfiguration{}
		if err := yaml.Unmarshal(document, configuration); err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		local[configuration.Name] = configuration
	}

	for name, chartConfiguration := range chart {
		localConfiguration, ok := local[name]
		if !ok {
			// The chart ships the warden and the kubecube configurations; the
			// local path applies both, so a missing one is drift.
			t.Errorf("the chart ships %s, which the local install path does not apply", name)
			continue
		}

		for webhook, chartWebhook := range byWebhookName(chartConfiguration) {
			localWebhook, ok := byWebhookName(localConfiguration)[webhook]
			if !ok {
				t.Errorf("%s: the chart has webhook %s and the local file does not", name, webhook)
				continue
			}

			if failurePolicyOf(chartWebhook) != failurePolicyOf(localWebhook) {
				t.Errorf("%s: %s is %s in the chart and %s locally",
					name, webhook, failurePolicyOf(chartWebhook), failurePolicyOf(localWebhook))
			}
			if (chartWebhook.NamespaceSelector != nil) != (localWebhook.NamespaceSelector != nil) {
				t.Errorf("%s: %s carries a namespaceSelector in one of the two and not the other", name, webhook)
			}
		}
	}
}

// assertWebhookBoundaries states the invariants the webhook surface is allowed
// to have.
func assertWebhookBoundaries(t *testing.T, source string, configuration *admissionv1.ValidatingWebhookConfiguration) {
	t.Helper()

	for _, violation := range webhookViolations(configuration) {
		t.Errorf("%s: %s", source, violation)
	}
}

// webhookViolations answers what is wrong with one configuration rather than
// reporting it, so the rule itself can be tested against inputs that break it.
func webhookViolations(configuration *admissionv1.ValidatingWebhookConfiguration) []string {
	var violations []string

	for _, webhook := range configuration.Webhooks {
		core := false
		for _, rule := range webhook.Rules {
			for _, group := range rule.APIGroups {
				if group == "" {
					core = true
				}
			}
		}

		if !core {
			// The platform's own webhooks act on the platform's own objects.
			continue
		}

		// One webhook is allowed to act on a core object, and only under these
		// conditions: it is the ResourceQuota one, it is limited to the
		// namespaces the platform owns, and a failure to reach the webhook lets
		// the write through rather than stopping the cluster.
		if !isResourceQuota(webhook) {
			violations = append(violations, fmt.Sprintf(
				"webhook %s acts on a core object, and only the ResourceQuota one may", webhook.Name))
			continue
		}
		if failurePolicyOf(webhook) != admissionv1.Ignore {
			violations = append(violations, fmt.Sprintf(
				"webhook %s is %s, and a webhook over a core object must not stop the cluster",
				webhook.Name, failurePolicyOf(webhook)))
		}
		if webhook.NamespaceSelector == nil {
			violations = append(violations, fmt.Sprintf(
				"webhook %s has no namespaceSelector, so it intercepts every ResourceQuota in the cluster",
				webhook.Name))
			continue
		}
		if !selectsPlatformNamespaces(webhook) {
			violations = append(violations, fmt.Sprintf(
				"webhook %s selects namespaces the platform does not own", webhook.Name))
		}
	}

	return violations
}

// TestTheBoundaryRuleItself feeds the rule the three ways a webhook over a core
// object can be wrong, so that a passing check is a check that can fail.
func TestTheBoundaryRuleItself(t *testing.T) {
	core := []admissionv1.RuleWithOperations{{
		Operations: []admissionv1.OperationType{admissionv1.Create},
		Rule:       admissionv1.Rule{APIGroups: []string{""}, Resources: []string{"resourcequotas"}},
	}}
	ignore := admissionv1.Ignore
	fail := admissionv1.Fail
	selector := &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
		{Key: "kubecube.io/namespace-owner", Operator: metav1.LabelSelectorOpExists},
	}}

	tests := []struct {
		name       string
		webhook    admissionv1.ValidatingWebhook
		violations int
	}{
		{
			name: "a core webhook that is not the ResourceQuota one",
			webhook: admissionv1.ValidatingWebhook{
				Name: "vsomething.kb.io", FailurePolicy: &ignore, NamespaceSelector: selector,
				Rules: []admissionv1.RuleWithOperations{{
					Operations: []admissionv1.OperationType{admissionv1.Create},
					Rule:       admissionv1.Rule{APIGroups: []string{""}, Resources: []string{"pods"}},
				}},
			},
			violations: 1,
		},
		{
			name: "the ResourceQuota webhook refusing to let a write through",
			webhook: admissionv1.ValidatingWebhook{
				Name: "vresourcequota.kb.io", Rules: core, FailurePolicy: &fail, NamespaceSelector: selector,
			},
			violations: 1,
		},
		{
			name: "the ResourceQuota webhook with no namespace selector",
			webhook: admissionv1.ValidatingWebhook{
				Name: "vresourcequota.kb.io", Rules: core, FailurePolicy: &ignore,
			},
			violations: 1,
		},
		{
			name: "the ResourceQuota webhook selecting somebody else's namespaces",
			webhook: admissionv1.ValidatingWebhook{
				Name: "vresourcequota.kb.io", Rules: core, FailurePolicy: &ignore,
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}},
			},
			violations: 1,
		},
		{
			name: "the webhook the platform is allowed to have",
			webhook: admissionv1.ValidatingWebhook{
				Name: "vresourcequota.kb.io", Rules: core, FailurePolicy: &ignore, NamespaceSelector: selector,
			},
			violations: 0,
		},
		{
			name: "a webhook over the platform's own objects",
			webhook: admissionv1.ValidatingWebhook{
				Name: "vtenant.kb.io", FailurePolicy: &fail,
				Rules: []admissionv1.RuleWithOperations{{
					Operations: []admissionv1.OperationType{admissionv1.Create},
					Rule:       admissionv1.Rule{APIGroups: []string{"tenant.kubecube.io"}, Resources: []string{"tenants"}},
				}},
			},
			violations: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configuration := &admissionv1.ValidatingWebhookConfiguration{
				Webhooks: []admissionv1.ValidatingWebhook{tt.webhook},
			}

			violations := webhookViolations(configuration)
			if len(violations) != tt.violations {
				t.Errorf("violations = %v, want %d", violations, tt.violations)
			}
		})
	}
}

func isResourceQuota(webhook admissionv1.ValidatingWebhook) bool {
	for _, rule := range webhook.Rules {
		for _, resource := range rule.Resources {
			if resource == "resourcequotas" {
				return true
			}
		}
	}
	return false
}

func selectsPlatformNamespaces(webhook admissionv1.ValidatingWebhook) bool {
	for _, expression := range webhook.NamespaceSelector.MatchExpressions {
		if expression.Key == "kubecube.io/namespace-owner" {
			return true
		}
	}
	return false
}

func failurePolicyOf(webhook admissionv1.ValidatingWebhook) admissionv1.FailurePolicyType {
	if webhook.FailurePolicy == nil {
		return admissionv1.Fail
	}
	return *webhook.FailurePolicy
}

func byWebhookName(configuration *admissionv1.ValidatingWebhookConfiguration) map[string]admissionv1.ValidatingWebhook {
	webhooks := make(map[string]admissionv1.ValidatingWebhook, len(configuration.Webhooks))
	for _, webhook := range configuration.Webhooks {
		webhooks[webhook.Name] = webhook
	}
	return webhooks
}

// chartWebhookConfigurations reads the two configurations the chart ships,
// which live as text inside the pre-install job's ConfigMap.
func chartWebhookConfigurations(t *testing.T) map[string]*admissionv1.ValidatingWebhookConfiguration {
	t.Helper()

	path := os.Getenv("KUBECUBE_RENDERED_CHART")
	if path == "" {
		t.Skip("set KUBECUBE_RENDERED_CHART to the chart rendered by helm template")
	}

	var static string
	for _, document := range renderedDocuments(t, path) {
		if kind, _ := document["kind"].(string); kind != "ConfigMap" {
			continue
		}
		metadata, _ := document["metadata"].(map[string]interface{})
		if name, _ := metadata["name"].(string); name != "certs-config" {
			continue
		}
		data, _ := document["data"].(map[string]interface{})
		static, _ = data["static-resources-configmaps.yaml"].(string)
	}
	if static == "" {
		t.Fatal("the chart no longer renders the webhook configurations in the pre-install job, so nothing was checked")
	}

	holder := struct {
		Data map[string]string `json:"data"`
	}{}
	if err := yaml.Unmarshal([]byte(static), &holder); err != nil {
		t.Fatalf("reading the pre-install job's static resources: %v", err)
	}

	configurations := map[string]*admissionv1.ValidatingWebhookConfiguration{}
	for name, document := range holder.Data {
		if !strings.Contains(document, "kind: ValidatingWebhookConfiguration") {
			continue
		}

		// The job substitutes the CA into the bundle at install time.
		document = caBundleLine.ReplaceAllString(document, "${1}"+caBundlePlaceholder)

		configuration := &admissionv1.ValidatingWebhookConfiguration{}
		if err := yaml.Unmarshal([]byte(document), configuration); err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		configurations[configuration.Name] = configuration
	}

	if len(configurations) == 0 {
		t.Fatal("the chart's static resources hold no webhook configuration, so nothing was checked")
	}

	return configurations
}

// TestNoCRDIsConvertedByAWebhook is the third invariant: a conversion webhook is
// a call from the API server into the platform on a path nothing else exercises,
// and the platform does not serve one.
func TestNoCRDIsConvertedByAWebhook(t *testing.T) {
	path := os.Getenv("KUBECUBE_RENDERED_CHART")
	if path == "" {
		t.Skip("set KUBECUBE_RENDERED_CHART to the chart rendered by helm template")
	}

	checked := 0
	for _, document := range renderedDocuments(t, path) {
		if kind, _ := document["kind"].(string); kind != "CustomResourceDefinition" {
			continue
		}
		checked++

		spec, _ := document["spec"].(map[string]interface{})
		conversion, _ := spec["conversion"].(map[string]interface{})
		if strategy, _ := conversion["strategy"].(string); strings.EqualFold(strategy, "Webhook") {
			metadata, _ := document["metadata"].(map[string]interface{})
			name, _ := metadata["name"].(string)
			t.Errorf("CustomResourceDefinition %s is converted by a webhook", name)
		}
	}

	if checked == 0 {
		t.Fatal("the chart renders no CustomResourceDefinition, so nothing was checked — render it with --include-crds")
	}
}
