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

package conversion

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kubecube-io/kubecube/pkg/clog"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
)

// VersionConverter knows how to convert object to specified version
type VersionConverter struct {
	// scheme hold full versions k8s api
	scheme *runtime.Scheme

	// cf the codec factory had methods of encode and decode
	cf serializer.CodecFactory

	// discovery is response to communicate with k8s
	discovery discovery.DiscoveryInterface

	// clusterInfo hold the version info of target cluster
	clusterInfo *version.Info

	// RestMapper is response to map gvk and gvr
	RestMapper meta.RESTMapper
}

// NewVersionConvertor create a version convert for a target cluster
func NewVersionConvertor(discovery discovery.DiscoveryInterface, restMapper meta.RESTMapper, installFuncs ...InstallFunc) (*VersionConverter, error) {
	scheme := runtime.NewScheme()
	install(scheme, installFuncs...)

	info, err := discovery.ServerVersion()
	if err != nil {
		return nil, err
	}

	if restMapper == nil {
		m := meta.NewDefaultRESTMapper(scheme.PrioritizedVersionsAllGroups())
		for gvk := range scheme.AllKnownTypes() {
			m.Add(gvk, nil)
		}
		restMapper = m
	}

	return &VersionConverter{
		scheme:      scheme,
		discovery:   discovery,
		RestMapper:  restMapper,
		clusterInfo: info,
		cf:          serializer.NewCodecFactory(scheme),
	}, nil
}

// Convert converts an Object to another, generally the conversion is internalVersion <-> versioned.
// if out was set, the converted result would be set into.
func (c *VersionConverter) Convert(in runtime.Object, out runtime.Object, target runtime.GroupVersioner) (runtime.Object, error) {
	if out != nil {
		if err := c.scheme.Convert(in, out, target); err != nil {
			return nil, err
		}
		return out, nil
	}
	return c.scheme.ConvertToVersion(in, target)
}

// DirectConvert converts a versioned Object to another version with given target gv.
// if out was set, the converted result would be set into.
func (c *VersionConverter) DirectConvert(in runtime.Object, out runtime.Object, target runtime.GroupVersioner) (runtime.Object, error) {
	internalObject, err := c.Convert(in, nil, runtime.InternalGroupVersioner)
	if err != nil {
		return nil, err
	}
	if out != nil {
		if err := c.scheme.Convert(internalObject, out, target); err != nil {
			return nil, err
		}
		return out, nil
	}
	return c.Convert(internalObject, nil, target)
}

// GreetBackType will tell what the way that obj can access cluster
type GreetBackType int

const (
	IsPassThrough GreetBackType = iota
	IsNotSupport
	IsNeedConvert
	IsUnknown
)

func (g GreetBackType) String() string {
	var greetBack string

	switch g {
	case 0:
		greetBack = "paas through"
	case 1:
		greetBack = "not support"
	case 2:
		greetBack = "need convert"
	case 3:
		greetBack = "unknown"
	}

	return greetBack
}

// ObjectGreeting describes if given object is available in target cluster.
// a recommend group version kind will return if it cloud not pass through.
func (c *VersionConverter) ObjectGreeting(obj runtime.Object) (greetBack GreetBackType, rawGvk *schema.GroupVersionKind, recommendGvk *schema.GroupVersionKind, err error) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Empty() {
		gvks, _, err := c.scheme.ObjectKinds(obj)
		if err != nil {
			return IsUnknown, nil, nil, err
		}
		gvk = gvks[0]
	}
	return c.GvkGreeting(&gvk)
}

// GvrGreeting describes if given gvr is available in target cluster.
// a recommend group version kind will return if it cloud not pass through.
func (c *VersionConverter) GvrGreeting(gvr *schema.GroupVersionResource) (greetBack GreetBackType, rawGvk *schema.GroupVersionKind, recommendGvk *schema.GroupVersionKind, err error) {
	gvk, err := Gvr2Gvk(c.RestMapper, gvr)
	if err != nil {
		return IsUnknown, gvk, nil, err
	}

	// use best priority
	return c.GvkGreeting(gvk)
}

// GvkGreeting describes if given gvk is available in target cluster.
// a recommend group version kind will return if it cloud not pass through.
//
// The recommendation is a deterministic function of what the cluster serves and
// of the api group's own declared preferred version. It deliberately does not
// depend on the order discovery returned resources in, and it stays inside the
// requested group when that group serves the kind, so that two groups serving
// the same kind cannot silently move an object between them.
func (c *VersionConverter) GvkGreeting(gvk *schema.GroupVersionKind) (greetBack GreetBackType, rawGvk *schema.GroupVersionKind, recommendGvk *schema.GroupVersionKind, err error) {
	clusterVersion := Version(c.clusterInfo)

	// treat xxxList as xxx Kind
	gvkCopy := &schema.GroupVersionKind{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind}
	if strings.HasSuffix(gvk.Kind, "List") {
		gvkCopy.Kind = strings.TrimSuffix(gvk.Kind, "List")
	}

	groups, allResources, err := c.discovery.ServerGroupsAndResources()
	if err != nil {
		// one unavailable api group must not hide the rest of the cluster's api surface
		clog.Warn("some api group is not available in target cluster, err: %s", err.Error())
	}

	served := servedResources(allResources)

	// the exact group/version/kind is served, nothing to adapt
	for _, r := range served {
		if r.group == gvkCopy.Group && r.version == gvkCopy.Version && r.kind == gvkCopy.Kind {
			return IsPassThrough, gvk, nil, nil
		}
	}

	// candidates are the versions that serve this kind at all
	candidates := make([]servedResource, 0, 4)
	for _, r := range served {
		if r.kind == gvkCopy.Kind {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		clog.Debug("%v is not support in target cluster %v", gvk.String(), clusterVersion)
		return IsNotSupport, gvk, nil, nil
	}

	// stay in the requested group when it serves the kind
	pool := candidates
	sameGroup := make([]servedResource, 0, len(candidates))
	for _, r := range candidates {
		if r.group == gvkCopy.Group {
			sameGroup = append(sameGroup, r)
		}
	}
	if len(sameGroup) > 0 {
		pool = sameGroup
	}

	best := pickServed(pool, preferredVersions(groups))

	return IsNeedConvert, gvk, &schema.GroupVersionKind{Group: best.group, Version: best.version, Kind: gvk.Kind}, nil
}

// servedResource is one group/version/kind the target cluster serves.
type servedResource struct {
	group   string
	version string
	kind    string
}

// servedResources flattens the discovery result into one entry per served
// group/version/kind, filling in the group and version that some servers omit
// on each individual APIResource.
func servedResources(allResources []*metav1.APIResourceList) []servedResource {
	served := make([]servedResource, 0, 64)
	for _, gvs := range allResources {
		if gvs == nil {
			continue
		}
		gv, err := schema.ParseGroupVersion(gvs.GroupVersion)
		if err != nil {
			clog.Warn("parse group version %v failed: %v", gvs.GroupVersion, err)
			continue
		}
		for _, resource := range gvs.APIResources {
			group, version := resource.Group, resource.Version
			if group == "" {
				group = gv.Group
			}
			if version == "" {
				version = gv.Version
			}
			served = append(served, servedResource{group: group, version: version, kind: resource.Kind})
		}
	}
	return served
}

// preferredVersions maps an api group to the version it declares as preferred,
// which is the version Kubernetes itself resolves a group-only request to.
func preferredVersions(groups []*metav1.APIGroup) map[string]string {
	preferred := make(map[string]string, len(groups))
	for _, g := range groups {
		if g == nil || g.PreferredVersion.Version == "" {
			continue
		}
		preferred[g.Name] = g.PreferredVersion.Version
	}
	return preferred
}

// pickServed chooses one entry deterministically:
//
//  1. the group's own preferred version, when it serves the kind
//  2. a stable version (vN) over a pre-release one
//  3. the highest minor
//  4. lexicographic version, as the final tie-break
//
// The pool is already restricted to one group whenever the requested group
// serves the kind, so comparing the group first only decides the case where the
// requested group does not serve it at all.
func pickServed(pool []servedResource, preferred map[string]string) servedResource {
	sorted := make([]servedResource, len(pool))
	copy(sorted, pool)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.group != b.group {
			return a.group < b.group
		}
		if pv := preferred[a.group]; pv != "" {
			if ap, bp := a.version == pv, b.version == pv; ap != bp {
				return ap
			}
		}
		if as, bs := isStable(a.version), isStable(b.version); as != bs {
			return as
		}
		if am, bm := versionMinor(a.version), versionMinor(b.version); am != bm {
			return am > bm
		}
		return a.version < b.version
	})
	return sorted[0]
}

func isStable(version string) bool {
	return IsStableVersion(schema.GroupVersion{Version: version})
}

// versionMinor extracts the numeric minor from a version string such as v1,
// v1beta1 or v2alpha1, returning 0 when there is none to parse.
func versionMinor(version string) int {
	m := versionMinorRegexp.FindStringSubmatch(version)
	if m == nil {
		return 0
	}
	minor, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return minor
}

var versionMinorRegexp = regexp.MustCompile(`^v(\d+)`)

// Encode encodes given obj, generally the gv should match Object
func (c *VersionConverter) Encode(obj runtime.Object, gv runtime.GroupVersioner) ([]byte, error) {
	info, ok := runtime.SerializerInfoForMediaType(c.cf.SupportedMediaTypes(), runtime.ContentTypeJSON)
	if !ok {
		return nil, errors.New("no media type match for serializer")
	}
	encoder := info.Serializer
	codec := c.cf.EncoderForVersion(encoder, gv)
	out, err := runtime.Encode(codec, obj)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Decode decodes data to object, if defaults was not set, the internalVersion would be used.
func (c *VersionConverter) Decode(data []byte, defaults *schema.GroupVersionKind, into runtime.Object, versions ...schema.GroupVersion) (runtime.Object, *schema.GroupVersionKind, error) {
	decoder := c.cf.UniversalDecoder(versions...)
	return decoder.Decode(data, defaults, into)
}

// Gvr2Gvk convert gvr to gvk by specified cluster
func Gvr2Gvk(mapper meta.RESTMapper, gvr *schema.GroupVersionResource) (*schema.GroupVersionKind, error) {
	kinds, err := mapper.KindsFor(*gvr)
	if err != nil {
		return nil, err
	}

	if len(kinds) == 0 {
		return nil, fmt.Errorf("%v is not supportted", gvr.String())
	}

	// use best priority
	return &kinds[0], nil
}

// Gvk2Gvr convert gvk to gvr by specified cluster
func Gvk2Gvr(mapper meta.RESTMapper, gvk *schema.GroupVersionKind) (*schema.GroupVersionResource, error) {
	m, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}

	return &m.Resource, nil
}

// ConvertURL convert url by given gvr
func ConvertURL(url string, gvr *schema.GroupVersionResource) (convertedUrl string, err error) {
	const sep = "/"

	rawIsCoreApi, _, _, err := ParseURL(url)
	if err != nil {
		return "", err
	}

	isCoreApi := gvr.Group == ""

	ss := strings.Split(strings.TrimPrefix(url, sep), sep)

	switch {
	case isCoreApi && rawIsCoreApi:
		ss[1] = gvr.Version
	case !isCoreApi && rawIsCoreApi:
		ss[0] = "apis"
		ss[1] = gvr.Group + sep + gvr.Version
	case isCoreApi && !rawIsCoreApi:
		// /apis/batch/v1/namespaces/{namespace}/jobs
		ss[0] = "api"
		ss[2] = gvr.Version
		ss = append(ss[:1], ss[2:]...)
	case !isCoreApi && !rawIsCoreApi:
		ss[1] = gvr.Group
		ss[2] = gvr.Version
	}

	return sep + strings.Join(ss, "/"), nil
}

// ParseURL parse k8s api url into gvr
func ParseURL(url string) (bool, bool, *schema.GroupVersionResource, error) {
	invalidUrlErr := fmt.Errorf("url not k8s format: %s", url)

	const (
		coreApiPrefix    = "/api/"
		nonCoreApiPrefix = "/apis/"
		nsSubString      = "/namespaces/"
	)

	isCoreApi, isNonCoreApi := strings.HasPrefix(url, coreApiPrefix), strings.HasPrefix(url, nonCoreApiPrefix)

	ss := strings.Split(strings.TrimPrefix(url, "/"), "/")
	var isNamespaced bool
	if len(ss) > 4 && strings.Contains(url, nsSubString) {
		isNamespaced = true
	}

	gvr := &schema.GroupVersionResource{}
	switch {
	case isCoreApi && isNamespaced:
		// like: /api/v1/namespaces/{namespace}/pods
		if len(ss) < 5 {
			return false, false, nil, invalidUrlErr
		}
		gvr.Version = ss[1]
		gvr.Resource = ss[4]
	case isCoreApi && !isNamespaced:
		// like: /api/v1/namespaces/{name}
		if len(ss) < 3 {
			return false, false, nil, invalidUrlErr
		}
		gvr.Version = ss[1]
		gvr.Resource = ss[2]
	case isNonCoreApi && isNamespaced:
		// like: /apis/batch/v1/namespaces/{namespace}/jobs
		if len(ss) < 6 {
			return false, false, nil, invalidUrlErr
		}
		gvr.Group = ss[1]
		gvr.Version = ss[2]
		gvr.Resource = ss[5]
	case isNonCoreApi && !isNamespaced:
		// like: /apis/rbac.authorization.k8s.io/v1/clusterroles
		if len(ss) < 4 {
			return false, false, nil, invalidUrlErr
		}
		gvr.Group = ss[1]
		gvr.Version = ss[2]
		gvr.Resource = ss[3]
	default:
		return false, false, nil, invalidUrlErr
	}

	return isCoreApi, isNamespaced, gvr, nil
}

var stableVersionRegexp = regexp.MustCompile(`^v[0-9]+$`)

// IsStableVersion tells if given gv is stable, meaning a released version (vN)
// rather than a pre-release one (vNalphaM, vNbetaM). The api group is
// irrelevant: apps/v1, batch/v1 and networking.k8s.io/v1 are all stable, and
// recognising only the core group made the preference unusable everywhere it
// mattered.
func IsStableVersion(gv schema.GroupVersion) bool {
	return stableVersionRegexp.MatchString(gv.Version)
}

// Version print cluster version info
func Version(info *version.Info) string {
	return fmt.Sprintf("%v.%v", info.Major, info.Minor)
}

// ParseVersion parse version to currentMajor and currentMinor
//
// only two format is valid, example:
// 1. v1.19
// 2. 1.19
func ParseVersion(version string) (currentMajor, currentMinor int, err error) {
	invalidError := errors.New("invalid version")

	if len(version) == 0 {
		return 0, 0, invalidError
	}

	v := strings.TrimLeft(version, "v")
	vs := strings.Split(v, ".")

	if len(vs) != 2 {
		return 0, 0, invalidError
	}

	currentMajor, err = strconv.Atoi(vs[0])
	if err != nil {
		return 0, 0, invalidError
	}

	currentMinor, err = strconv.Atoi(vs[1])
	if err != nil {
		return 0, 0, invalidError
	}

	return
}

// VersionCompare compare the both versions
// return 1 means v1 > v2
// return 0 means v1 = v2
// return -1 means v1 < v2
func VersionCompare(v1, v2 string) (int, error) {
	majorV1, minorV1, err := ParseVersion(v1)
	if err != nil {
		return 0, err
	}

	majorV2, minorV2, err := ParseVersion(v2)
	if err != nil {
		return 0, err
	}

	switch {
	case (majorV1 > majorV2) || (majorV1 == majorV2 && minorV1 > minorV2):
		return 1, nil
	case (majorV1 == majorV2) && (minorV1 == minorV2):
		return 0, nil
	default:
		return -1, nil
	}
}

// MarshalJSON marshal Unstructured into bytes
func MarshalJSON(u *unstructured.Unstructured) ([]byte, error) {
	return u.MarshalJSON()
}

// UnmarshalJSON unmarshal bytes into Unstructured
func UnmarshalJSON(b []byte) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{}
	err := u.UnmarshalJSON(b)
	if err != nil {
		return nil, err
	}
	return u, nil
}
