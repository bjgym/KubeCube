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

package alertconfig

import (
	"reflect"
	"testing"
)

func TestSecretReferences(t *testing.T) {
	tests := []struct {
		name string
		spec interface{}
		want []string
	}{
		{
			name: "a wechat receiver names its credential secret",
			spec: map[string]interface{}{
				"receivers": []interface{}{
					map[string]interface{}{
						"name": "ops",
						"wechatConfigs": []interface{}{
							map[string]interface{}{
								"apiSecret": map[string]interface{}{"name": "wechat-token", "key": "token"},
							},
						},
					},
				},
			},
			want: []string{"wechat-token"},
		},
		{
			name: "an email receiver names its auth secret",
			spec: map[string]interface{}{
				"receivers": []interface{}{
					map[string]interface{}{
						"name": "mail",
						"emailConfigs": []interface{}{
							map[string]interface{}{
								"authSecret": map[string]interface{}{"name": "smtp-auth", "key": "password"},
							},
						},
					},
				},
			},
			want: []string{"smtp-auth"},
		},
		{
			name: "a webhook receiver can name several secrets, nested in its http config",
			spec: map[string]interface{}{
				"receivers": []interface{}{
					map[string]interface{}{
						"name": "hook",
						"webhookConfigs": []interface{}{
							map[string]interface{}{
								"httpConfig": map[string]interface{}{
									"basicAuth": map[string]interface{}{
										"password": map[string]interface{}{"name": "hook-password", "key": "password"},
									},
									"authorization": map[string]interface{}{
										"credentials": map[string]interface{}{"name": "hook-token", "key": "token"},
									},
								},
							},
						},
					},
				},
			},
			want: []string{"hook-password", "hook-token"},
		},
		{
			// a route matcher carries name with value; reading it as a secret
			// would copy an object that does not exist and never converge
			name: "a route matcher is not a secret reference",
			spec: map[string]interface{}{
				"route": map[string]interface{}{
					"receiver": "ops",
					"matchers": []interface{}{
						map[string]interface{}{"name": "kubecube_io_owner", "value": "t1-p1-ops"},
					},
				},
			},
			want: []string{},
		},
		{
			name: "a receiver name is not a secret reference",
			spec: map[string]interface{}{
				"receivers": []interface{}{
					map[string]interface{}{"name": "ops"},
				},
			},
			want: []string{},
		},
		{
			name: "the same secret named twice is reported once",
			spec: map[string]interface{}{
				"receivers": []interface{}{
					map[string]interface{}{
						"wechatConfigs": []interface{}{
							map[string]interface{}{"apiSecret": map[string]interface{}{"name": "shared", "key": "a"}},
						},
						"emailConfigs": []interface{}{
							map[string]interface{}{"authSecret": map[string]interface{}{"name": "shared", "key": "b"}},
						},
					},
				},
			},
			want: []string{"shared"},
		},
		{
			name: "an absent spec names nothing",
			spec: nil,
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := secretReferences(tt.spec); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("secretReferences() = %v, want %v", got, tt.want)
			}
		})
	}
}
