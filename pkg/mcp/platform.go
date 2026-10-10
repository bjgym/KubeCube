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

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kubecube-io/kubecube/pkg/utils/errcode"
)

// defaultPlatformTimeout bounds one call to the platform, so a platform that
// stops answering does not hold a tool call open forever.
const defaultPlatformTimeout = 60 * time.Second

// maxPlatformAnswerBytes bounds one answer, so a mistaken endpoint cannot make
// the surface hold an unbounded body.
const maxPlatformAnswerBytes = 4 << 20

// tokenKey is the context key a request's token travels under.
type tokenKey struct{}

// WithToken returns a context carrying the token a request was established
// from.
//
// The token travels in the context rather than in Session because it is a
// credential: Session is a value that gets logged, compared and printed in
// test failures, and a bearer token in it would leak into all three.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, token)
}

// TokenFrom reads the token a request was established from.
func TokenFrom(ctx context.Context) (string, error) {
	token, ok := ctx.Value(tokenKey{}).(string)
	if !ok || token == "" {
		return "", fmt.Errorf("this call carries no session token")
	}
	return token, nil
}

// PlatformRefusal is the platform declining to do something for this session.
//
// It is a distinct type because the model has to be able to tell it from a
// failure: a refusal is an answer, and repeating the call will be refused
// again, while a failure is worth retrying.
type PlatformRefusal struct {
	Status int
	Reason string
}

func (e *PlatformRefusal) Error() string {
	return fmt.Sprintf("the platform refused this call (%d): %s", e.Status, e.Reason)
}

// PlatformFailure is the platform not answering, or answering that it broke.
type PlatformFailure struct {
	Status int
	Err    error
}

func (e *PlatformFailure) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("the platform could not be reached: %s", e.Err)
	}
	return fmt.Sprintf("the platform failed (%d): %s", e.Status, e.Err)
}

func (e *PlatformFailure) Unwrap() error { return e.Err }

// Route is one endpoint of the platform's API.
type Route struct {
	Method string

	// Path builds the path for one call. It is a function because a path
	// carries identifiers that come out of the arguments.
	Path func(session Session, arguments json.RawMessage) (string, error)
}

// Platform calls the platform's API as the session whose token the request
// carried.
//
// It holds no credential of its own: every call presents the session's token,
// so the platform authorizes the session rather than trusting this surface.
type Platform struct {
	// BaseURL is the platform's API root, without a trailing slash.
	BaseURL string

	// Client is the HTTP client. A nil one is replaced with a client that has a
	// timeout.
	Client *http.Client

	// Routes says where each tool's call goes. It is empty until the platform's
	// API for these tools is defined, and a tool with no route is refused
	// rather than guessed at: an endpoint this surface invented would look like
	// a working one.
	Routes map[string]Route
}

// Call serves one tool call, which is what makes Platform a Handler.
func (p Platform) Call(ctx context.Context, session Session, tool Tool, arguments json.RawMessage) (interface{}, error) {
	route, ok := p.Routes[tool.Name]
	if !ok {
		return nil, fmt.Errorf("the platform's API for %s is not defined yet, so this tool cannot be served", tool.Name)
	}

	path, err := route.Path(session, arguments)
	if err != nil {
		return nil, fmt.Errorf("building the call for %s: %w", tool.Name, err)
	}

	var out interface{}
	if err := p.Do(ctx, route.Method, path, arguments, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Do performs one call to the platform and decodes the answer into out.
//
// A call without the session's token is refused here rather than sent: a
// request that reaches the platform unauthenticated would be answered as an
// anonymous caller, and whatever it returned would be nobody's scope.
func (p Platform) Do(ctx context.Context, method, path string, body, out interface{}) error {
	token, err := TokenFrom(ctx)
	if err != nil {
		return err
	}
	if p.BaseURL == "" {
		return fmt.Errorf("this surface has no platform to call")
	}

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding the call: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	url := strings.TrimSuffix(p.BaseURL, "/") + "/" + strings.TrimPrefix(path, "/")
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("building the call: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: defaultPlatformTimeout}
	}

	response, err := client.Do(request)
	if err != nil {
		return &PlatformFailure{Err: err}
	}
	defer response.Body.Close()

	answer, err := io.ReadAll(io.LimitReader(response.Body, maxPlatformAnswerBytes))
	if err != nil {
		return &PlatformFailure{Status: response.StatusCode, Err: err}
	}

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return platformError(response.StatusCode, answer)
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return &PlatformFailure{Status: response.StatusCode, Err: fmt.Errorf("the answer could not be read: %w", err)}
	}

	return nil
}

// platformError turns a status and a body into a refusal or a failure.
//
// The split is the one the model needs: a refusal is a decision about this
// session, and a failure is the platform not deciding at all. The platform's
// own message is carried through, because it is written for a person and this
// surface has nothing better to add.
func platformError(status int, body []byte) error {
	reason := ""
	info := &errcode.ErrorInfo{}
	if err := json.Unmarshal(body, info); err == nil {
		reason = info.Message
	}

	switch {
	case status == http.StatusUnauthorized:
		return &PlatformRefusal{Status: status, Reason: "the platform did not accept this session's token"}

	case status == http.StatusForbidden,
		status == http.StatusNotFound,
		status == http.StatusConflict,
		status == http.StatusUnprocessableEntity:
		return &PlatformRefusal{Status: status, Reason: orDefault(reason, "the platform declined the call")}

	case status == http.StatusTooManyRequests:
		return &PlatformFailure{Status: status, Err: errors.New("the platform is rate limiting this surface")}

	case status >= 500:
		return &PlatformFailure{Status: status, Err: errors.New(orDefault(reason, "the platform reported a failure"))}

	default:
		return &PlatformFailure{Status: status, Err: errors.New(orDefault(reason, "the platform rejected the call as malformed"))}
	}
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
