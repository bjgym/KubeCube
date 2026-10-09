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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/kubecube-io/kubecube/pkg/authentication/identityprovider"
	"github.com/kubecube-io/kubecube/pkg/clog"
)

// Field aliases, resolved in order with the first non-empty value winning.
// OAuth2 does not standardise the userinfo payload, so the common spellings are
// accepted instead of requiring one provider's exact schema.
var (
	accountIdFields = []string{"sub", "id", "accountId", "userId", "account"}
	userNameFields  = []string{"nickname", "name", "displayName", "preferred_username", "username"}
	userEmailFields = []string{"email", "mail"}
)

type ssoProvider struct {
	ClientID     string
	ClientSecret string
	SsoIsEnable  bool
	SsoTokenUrl  string
	SsoUserUrl   string
}

// identity is the normalised profile the provider returns.
type identity struct {
	AccountID string
	UserName  string
	Email     string
}

func (i identity) GetRespHeader() http.Header {
	return nil
}

func (i identity) GetUserName() string {
	return i.UserName
}

func (i identity) GetGroup() string {
	return ""
}

func (i identity) GetUserEmail() string {
	return i.Email
}

func (i identity) GetAccountId() string {
	return i.AccountID
}

// GetProvider builds the sso provider for the provider key name found in the
// kubecube-auth-config ConfigMap and Secret.
func GetProvider(name string) ssoProvider {
	config := getConfig(name)
	return ssoProvider{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		SsoIsEnable:  config.IsEnable,
		SsoTokenUrl:  config.TokenUrl,
		SsoUserUrl:   config.UserUrl,
	}
}

// IdentityExchange trades the authorization code for an access token and then
// reads the user profile with it. Both urls are format templates supplied by the
// provider configuration, taking (clientID, clientSecret, code) and
// (accessToken) respectively.
func (g *ssoProvider) IdentityExchange(code string) (identityprovider.Identity, error) {
	if g.ClientID == "" || g.ClientSecret == "" || g.SsoTokenUrl == "" {
		clog.Error("clientId or clientSecret is null")
		return nil, errors.New("clientId or clientSecret is null")
	}

	// get token
	url := fmt.Sprintf(g.SsoTokenUrl, g.ClientID, g.ClientSecret, code)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		clog.Error("new http post request err: %v", err)
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		clog.Error("request to sso for token error: %v", err)
		return nil, err
	}
	respBody, err := readBody(resp)
	if err != nil {
		clog.Error("read token response error: %v", err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		clog.Error("token endpoint returned %d: %s", resp.StatusCode, string(respBody))
		return nil, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	accessToken, err := extractAccessToken(respBody)
	if err != nil {
		return nil, err
	}

	// get user info by token
	url = fmt.Sprintf(g.SsoUserUrl, accessToken)
	req, err = http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		clog.Error("new http get request err: %v", err)
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	// RFC 6750 scheme; providers that expect the GitHub style `token` prefix
	// should expose a token url that already carries the token instead.
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err = client.Do(req)
	if err != nil {
		clog.Error("request to sso for user info error: %v", err)
		return nil, err
	}
	respBody, err = readBody(resp)
	if err != nil {
		clog.Error("read user response error: %v", err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		clog.Error("user endpoint returned %d: %s", resp.StatusCode, string(respBody))
		return nil, fmt.Errorf("user endpoint returned %d", resp.StatusCode)
	}

	profile, err := parseIdentity(respBody)
	if err != nil {
		clog.Error("parse user response error: %v", err)
		return nil, err
	}
	if profile.AccountID == "" {
		return nil, errors.New("user response carries no account id")
	}

	return profile, nil
}

func readBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// extractAccessToken reads the access token from a token response, accepting
// both the flat OAuth2 shape and a `data`-wrapped envelope. A non-zero `code`,
// when the provider sends one, is reported as an error.
func extractAccessToken(respBody []byte) (string, error) {
	payload, err := decodeObject(respBody)
	if err != nil {
		return "", err
	}
	if code, ok := payload["code"].(float64); ok && code != 0 {
		return "", fmt.Errorf("token response code is not 0: %v", payload["message"])
	}
	if token, ok := payload["access_token"].(string); ok && token != "" {
		return token, nil
	}
	if data, ok := payload["data"].(map[string]interface{}); ok {
		if token, ok := data["access_token"].(string); ok && token != "" {
			return token, nil
		}
	}
	return "", errors.New("token response carries no access_token")
}

// parseIdentity normalises the userinfo payload. Both a flat OIDC response and a
// `data`-wrapped envelope are accepted.
func parseIdentity(respBody []byte) (identity, error) {
	payload, err := decodeObject(respBody)
	if err != nil {
		return identity{}, err
	}
	if code, ok := payload["code"].(float64); ok && code != 0 {
		return identity{}, fmt.Errorf("user response code is not 0: %v", payload["message"])
	}
	profile := payload
	if data, ok := payload["data"].(map[string]interface{}); ok {
		profile = data
	}
	return identity{
		AccountID: firstString(profile, accountIdFields...),
		UserName:  firstString(profile, userNameFields...),
		Email:     firstString(profile, userEmailFields...),
	}, nil
}

func decodeObject(body []byte) (map[string]interface{}, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, errors.New("empty json response")
	}
	return payload, nil
}

func firstString(profile map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := profile[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
