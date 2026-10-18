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

package cluster

import (
	"encoding/base64"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	userinfo "k8s.io/apiserver/pkg/authentication/user"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1 "github.com/kubecube-io/kubecube/pkg/apis/cluster/v1"
	quotav1 "github.com/kubecube-io/kubecube/pkg/apis/quota/v1"
	tenantv1 "github.com/kubecube-io/kubecube/pkg/apis/tenant/v1"
	userv1 "github.com/kubecube-io/kubecube/pkg/apis/user/v1"
	"github.com/kubecube-io/kubecube/pkg/authorizer/rbac"
	"github.com/kubecube-io/kubecube/pkg/clients"
	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/multicluster"
	mgrclient "github.com/kubecube-io/kubecube/pkg/multicluster/client"
	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/quota"
	"github.com/kubecube-io/kubecube/pkg/utils/access"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
	"github.com/kubecube-io/kubecube/pkg/utils/errcode"
	"github.com/kubecube-io/kubecube/pkg/utils/kubeconfig"
	"github.com/kubecube-io/kubecube/pkg/utils/response"
	"github.com/kubecube-io/kubecube/pkg/utils/transition"
)

const subPath = "/clusters"

func (h *handler) AddApisTo(root *gin.Engine) {
	r := root.Group(constants.ApiPathRoot + subPath)
	r.GET("info", h.getClusterInfo)
	r.GET("/:cluster/monitor", h.getClusterMonitorInfo)
	r.GET("/:cluster/livedata", h.getClusterLivedata)
	r.GET("namespaces", h.getClusterNames)
	r.GET("resources", h.getClusterResource)
	r.GET("subnamespaces", h.getSubNamespaces)
	r.POST("register", h.registerCluster)
	r.POST("add", h.addCluster)
	r.POST("nsquota", h.createNsAndQuota)
	r.POST("nsquota/onboard", h.onboardNamespace)
	r.GET("cuberesourcequotas", h.getCubeResourceQuota)
}

type result struct {
	Total int           `json:"total"`
	Items []clusterInfo `json:"items"`
}

// clusterInfo contains meta info and livedata info
type clusterInfo struct {
	clusterMetaInfo
	clusterLivedataInfo
}

type clusterMetaInfo struct {
	ClusterName         string            `json:"clusterName"`
	ClusterDescription  string            `json:"clusterDescription"`
	NetworkType         string            `json:"networkType"`
	HarborAddr          string            `json:"harborAddr"`
	IsMemberCluster     bool              `json:"isMemberCluster"`
	IsWritable          bool              `json:"isWritable"`
	CreateTime          time.Time         `json:"createTime"`
	KubeApiServer       string            `json:"kubeApiServer"`
	Status              string            `json:"status"`
	IngressDomainSuffix string            `json:"ingressDomainSuffix,omitempty"`
	Labels              map[string]string `json:"labels,omitempty"`
	Annotations         map[string]string `json:"annotations,omitempty"`
}

type clusterLivedataInfo struct {
	NodeCount             int `json:"nodeCount"`
	NamespaceCount        int `json:"namespaceCount"`
	UsedCPU               int `json:"usedCpu"`
	TotalCPU              int `json:"totalCpu"`
	UsedMem               int `json:"usedMem"`
	TotalMem              int `json:"totalMem"`
	TotalStorage          int `json:"totalStorage"`
	UsedStorage           int `json:"usedStorage"`
	TotalStorageEphemeral int `json:"totalStorageEphemeral"`
	UsedStorageEphemeral  int `json:"usedStorageEphemeral"`
	TotalGpu              int `json:"totalGpu"`
	UsedGpu               int `json:"usedGpu"`
	UsedCPURequest        int `json:"usedCpuRequest"`
	UsedCPULimit          int `json:"usedCpuLimit"`
	UsedMemRequest        int `json:"usedMemRequest"`
	UsedMemLimit          int `json:"usedMemLimit"`
}

type handler struct {
	rbac.Interface
	mgrclient.Client
}

func NewHandler() *handler {
	h := new(handler)
	h.Interface = rbac.NewDefaultResolver(constants.LocalCluster)
	h.Client = clients.Interface().Kubernetes(constants.LocalCluster)
	return h
}

// getClusterInfo get cluster details by cluster name
// @Summary Show cluster info
// @Description get cluster info by cluster name or project name, non query params means all clusters info
// @Tags cluster
// @Param cluster query string false "cluster info search by cluster name"
// @Param project query string false "cluster info search by project name"
// @Param status query string false "cluster info search by cluster status"
// @Success 200 {object} result "{"total":3,"items":[{"clusterName":"member-1","clusterDescription":"this is member cluster","networkType":"calico","harborAddr":"","isMemberCluster":true,"createTime":"2022-05-06T11:33:15+08:00","kubeApiServer":"https://10.173.33.3:6443","status":"normal","nodeCount":1,"namespaceCount":19,"usedCpu":549,"totalCpu":8000,"usedMem":7276,"totalMem":16648,"totalStorage":0,"usedStorage":0,"totalStorageEphemeral":42208,"usedStorageEphemeral":0,"totalGpu":0,"usedGpu":0,"usedCpuRequest":3300,"usedCpuLimit":4200,"usedMemRequest":3874,"usedMemLimit":7265},{"clusterName":"pivot-cluster","clusterDescription":"There is a pivot cluster dating with KubeCube","networkType":"","harborAddr":"","isMemberCluster":false,"createTime":"2022-04-28T14:41:26+08:00","kubeApiServer":"10.173.33.2:6443","status":"normal","nodeCount":1,"namespaceCount":18,"usedCpu":886,"totalCpu":8000,"usedMem":8996,"totalMem":16648,"totalStorage":0,"usedStorage":0,"totalStorageEphemeral":42208,"usedStorageEphemeral":0,"totalGpu":0,"usedGpu":0,"usedCpuRequest":3000,"usedCpuLimit":3900,"usedMemRequest":3469,"usedMemLimit":6860},{"clusterName":"member-2","clusterDescription":"this is member cluster","networkType":"calico","harborAddr":"","isMemberCluster":true,"createTime":"2022-04-28T16:12:13+08:00","kubeApiServer":"10.173.33.4:6443","status":"normal","nodeCount":1,"namespaceCount":19,"usedCpu":929,"totalCpu":8000,"usedMem":7187,"totalMem":16648,"totalStorage":0,"usedStorage":0,"totalStorageEphemeral":42208,"usedStorageEphemeral":0,"totalGpu":0,"usedGpu":0,"usedCpuRequest":3000,"usedCpuLimit":3900,"usedMemRequest":3469,"usedMemLimit":6860}]}"
// @Failure 500 {object} errcode.ErrorInfo
// @Router /api/v1/cube/clusters/info  [get]
func (h *handler) getClusterInfo(c *gin.Context) {
	var (
		cli         = h.Client
		ctx         = c.Request.Context()
		clusterList = clusterv1.ClusterList{}

		pageNum  int
		pageSize int
		err      error
	)

	clusterName := c.Query("cluster")
	clusterStatus := c.Query("status")
	projectName := c.Query("project")
	nodeLabelSelector := c.Query("nodeLabelSelector")
	pruneInfo := c.Query("prune")

	// parse paginate params if had
	if c.Query("pageNum") != "" && c.Query("pageSize") != "" {
		pageNum, err = strconv.Atoi(c.Query("pageNum"))
		if err != nil {
			response.FailReturn(c, errcode.BadRequest(err))
			return
		}
		pageSize, err = strconv.Atoi(c.Query("pageSize"))
		if err != nil {
			response.FailReturn(c, errcode.BadRequest(err))
			return
		}
	}

	start := time.Now()

	switch {
	// find cluster by given name
	case len(clusterName) > 0:
		key := types.NamespacedName{Name: clusterName}
		cluster := clusterv1.Cluster{}
		err := cli.Direct().Get(ctx, key, &cluster)
		if err != nil {
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "get cluster failed: %v", err))
			return
		}
		clusterList.Items = []clusterv1.Cluster{cluster}
	// give back all clusters by default
	default:
		clusters := clusterv1.ClusterList{}
		err := cli.Direct().List(ctx, &clusters)
		if err != nil {
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "list cluster failed: %v", err))
			return
		}
		clusterList = clusters
	}

	clog.Info("list cluster len(%v) cost time: %v", len(clusterList.Items), time.Now().Sub(start))

	if len(projectName) > 0 {
		clusterList, err = filterClustersByProject(ctx, clusterList, projectName)
		if err != nil {
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "get clusters by given project %v failed: %v", projectName, err))
			return
		}
	}

	selector, err := labels.Parse(nodeLabelSelector)
	if err != nil {
		response.FailReturn(c, errcode.ParamsInvalid(err))
		return
	}

	opts := clusterInfoOpts{
		pruneInfo:         pruneInfo == "true",
		statusFilter:      clusterStatus,
		nodeLabelSelector: selector,
		pageNum:           pageNum,
		pageSize:          pageSize,
	}

	res := result{Total: len(clusterList.Items)}

	infos, err := makeClusterInfos(c.Request.Context(), clusterList, opts)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	res.Items = infos

	response.SuccessReturn(c, res)
	return
}

type monitorInfo struct {
	NamespaceCount        int `json:"namespaceCount"`
	NodeCount             int `json:"nodeCount"`
	TotalCPU              int `json:"totalCpu"`
	UsedCPU               int `json:"usedCpu"`
	TotalMem              int `json:"totalMem"`
	UsedMem               int `json:"usedMem"`
	TotalStorage          int `json:"totalStorage"`
	UsedStorage           int `json:"usedStorage"`
	TotalStorageEphemeral int `json:"totalStorageEphemeral"`
	UsedStorageEphemeral  int `json:"usedStorageEphemeral"`
	TotalGpu              int `json:"totalGpu"`
	UsedGpu               int `json:"usedGpu"`
}

// getClusterMonitorInfo fetch resource used infos of specified cluster.
// temp not be used
func (h *handler) getClusterMonitorInfo(c *gin.Context) {
	cluster := c.Param("cluster")
	if len(cluster) == 0 {
		response.FailReturn(c, errcode.InvalidBodyFormat)
		return
	}

	info, err := makeMonitorInfo(c.Request.Context(), cluster)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	response.SuccessReturn(c, info)
}

// getClusterLivedata fetch livedata infos of specified cluster.
// temp not be used
func (h *handler) getClusterLivedata(c *gin.Context) {
	nodeLabelSelector := c.Query("nodeLabelSelector")
	cluster := c.Param("cluster")
	if len(cluster) == 0 {
		response.FailReturn(c, errcode.InvalidBodyFormat)
		return
	}

	selector, err := labels.Parse(nodeLabelSelector)
	if err != nil {
		response.FailReturn(c, errcode.ParamsInvalid(err))
		return
	}

	cli := clients.Interface().Kubernetes(cluster)
	if cli == nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "cluster %v abnormal", cluster))
		return
	}

	info, err := makeLivedataInfo(c.Request.Context(), cli, cluster, clusterInfoOpts{nodeLabelSelector: selector})
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	response.SuccessReturn(c, info)
}

// getClusterNames get cluster name where the namespace work in
// @Summary Show all clusters bind to namespace
// @Description get cluster name where the namespace work in
// @Tags cluster
// @Param namespace query string false "clusters search by namespace"
// @Success 200 {object} map[string]interface{} "{"items":["member-2","member-1","pivot-cluster"],"total":3}"
// @Failure 500 {object} errcode.ErrorInfo
// @Router /api/v1/cube/clusters/namespaces  [get]
func (h *handler) getClusterNames(c *gin.Context) {
	var (
		namespace    = c.Query("namespace")
		ctx          = c.Request.Context()
		clusterNames []string
	)

	if len(namespace) > 0 {
		clusters, err := getClustersByNamespace(namespace, ctx)
		if err != nil {
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
			return
		}
		clusterNames = clusters
	} else {
		clusterNames = listClusterNames()
	}

	sort.SliceStable(clusterNames, func(i, j int) bool {
		return clusterNames[i] < clusterNames[j]
	})

	res := map[string]interface{}{
		"total": len(clusterNames),
		"items": clusterNames,
	}

	response.SuccessReturn(c, res)
}

// getClusterResource get allocate resource of cluster
// @Summary Get allocate resource of cluster
// @Description get allocate resource of cluster
// @Tags cluster
// @Param cluster query string true "allocate resource search by cluster"
// @Success 200 {object} map[string]interface{} "{"assignedCpu":"4","assignedGpu":"0","assignedMem":"4000Mi","capacityCpu":"8","capacityGpu":"0","capacityMem":"15876Mi"}"
// @Failure 500 {object} errcode.ErrorInfo
// @Router /api/v1/cube/clusters/resources  [get]
func (h *handler) getClusterResource(c *gin.Context) {
	cluster := c.Query("cluster")
	nodeLabelSelector := c.Query("nodeLabelSelector")
	cli := clients.Interface().Kubernetes(cluster)
	if cli == nil {
		response.FailReturn(c, errcode.ClusterNotFoundError(cluster))
		return
	}

	selector, err := labels.Parse(nodeLabelSelector)
	if err != nil {
		response.FailReturn(c, errcode.ParamsInvalid(err))
		return
	}

	nodes := v1.NodeList{}
	err = cli.Cache().List(c.Request.Context(), &nodes, &client.ListOptions{LabelSelector: selector})
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "get cluster %v nodes failed: %v", cluster, err))
	}

	capacityCpu := quota.ZeroQ()
	capacityMem := quota.ZeroQ()
	capacityGpu := quota.ZeroQ()

	for _, v := range nodes.Items {
		capacityCpu.Add(*v.Status.Capacity.Cpu())
		capacityMem.Add(*v.Status.Capacity.Memory())
		nodeGpu, ok := v.Status.Capacity[constants.GpuNvidia]
		if ok {
			capacityGpu.Add(nodeGpu)
		}
	}

	assignedCpu, assignedMem, assignedGpu, err := getAssignedResource(cli, cluster)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	res := map[string]interface{}{
		"capacityCpu": capacityCpu,
		"assignedCpu": assignedCpu,
		"assignedMem": assignedMem,
		"capacityGpu": capacityGpu,
		"assignedGpu": assignedGpu,
		"capacityMem": capacityMem,
	}

	response.SuccessReturn(c, res)
}

type respBody struct {
	Namespace     string       `json:"namespace"`
	Cluster       string       `json:"cluster"`
	ClusterName   string       `json:"clusterName,omitempty"`
	Project       string       `json:"project"`
	Tenant        string       `json:"tenant"`
	NamespaceBody v1.Namespace `json:"namespaceBody"`

	// Unmanaged marks a namespace no tenant or project owns. It is only ever set
	// for a platform administrator, and it is what tells the console to show the
	// namespace without a project or a tenant rather than as a broken one.
	Unmanaged bool `json:"unmanaged,omitempty"`
}

// getSubNamespaces list sub namespace by tenant
// @Summary Get sub namespace
// @Description get sub namespaces by tenant
// @Tags cluster
// @Param tenant query string false "list sub namespaces by tenant"
// @Success 200 {object} map[string]interface{} "{"items":[{"namespace":"ns-3","cluster":"member-1","project":"project-2","namespaceBody":{"metadata":{"name":"ns-3","uid":"8b557f42-dcda-4555-bfba-4bc448b4d66f","resourceVersion":"862129","creationTimestamp":"2022-04-28T08:18:35Z","labels":{"hnc.x-k8s.io/included-namespace":"true","kubecube-project-project-2.tree.hnc.x-k8s.io/depth":"1","kubecube-tenant-tenant-2.tree.hnc.x-k8s.io/depth":"2","kubecube.hnc.x-k8s.io/project":"project-2","kubecube.hnc.x-k8s.io/tenant":"tenant-2","kubernetes.io/metadata.name":"ns-3","ns-3.tree.hnc.x-k8s.io/depth":"0"},"annotations":{"hnc.x-k8s.io/subnamespace-of":"kubecube-project-project-2"},"managedFields":[{"manager":"manager","operation":"Update","apiVersion":"v1","time":"2022-05-06T03:33:37Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:hnc.x-k8s.io/subnamespace-of":{}},"f:labels":{".":{},"f:kubecube-project-project-2.tree.hnc.x-k8s.io/depth":{},"f:kubecube-tenant-tenant-2.tree.hnc.x-k8s.io/depth":{},"f:kubecube.hnc.x-k8s.io/project":{},"f:kubecube.hnc.x-k8s.io/tenant":{},"f:kubernetes.io/metadata.name":{},"f:ns-3.tree.hnc.x-k8s.io/depth":{}}}}}]},"spec":{"finalizers":["kubernetes"]},"status":{"phase":"Active"}}},{"namespace":"ns-2","cluster":"member-2","project":"project-2","namespaceBody":{"metadata":{"name":"ns-2","uid":"8924ca69-c309-4e94-a55b-893b21ffde17","resourceVersion":"2270","creationTimestamp":"2022-04-28T08:18:23Z","labels":{"hnc.x-k8s.io/included-namespace":"true","kubecube-project-project-2.tree.hnc.x-k8s.io/depth":"1","kubecube-tenant-tenant-2.tree.hnc.x-k8s.io/depth":"2","kubecube.hnc.x-k8s.io/project":"project-2","kubecube.hnc.x-k8s.io/tenant":"tenant-2","kubernetes.io/metadata.name":"ns-2","ns-2.tree.hnc.x-k8s.io/depth":"0"},"annotations":{"hnc.x-k8s.io/subnamespace-of":"kubecube-project-project-2"},"managedFields":[{"manager":"manager","operation":"Update","apiVersion":"v1","time":"2022-04-28T08:18:23Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:hnc.x-k8s.io/subnamespace-of":{}},"f:labels":{".":{},"f:kubecube-project-project-2.tree.hnc.x-k8s.io/depth":{},"f:kubecube-tenant-tenant-2.tree.hnc.x-k8s.io/depth":{},"f:kubecube.hnc.x-k8s.io/project":{},"f:kubecube.hnc.x-k8s.io/tenant":{},"f:kubernetes.io/metadata.name":{},"f:ns-2.tree.hnc.x-k8s.io/depth":{}}}}}]},"spec":{"finalizers":["kubernetes"]},"status":{"phase":"Active"}}}],"total":2}"
// @Failure 500 {object} errcode.ErrorInfo
// @Router /api/v1/cube/clusters/subnamespaces  [get]
func (h *handler) getSubNamespaces(c *gin.Context) {
	fuzzyName := c.Query("fuzzyname")
	tenantArray := c.Query("tenant")
	tenantList := strings.Split(tenantArray, "|")
	if len(tenantArray) == 0 {
		tenantList = nil
	}
	ctx := c.Request.Context()
	clusters := multicluster.Interface().FuzzyCopy()

	userName := c.Query("user")
	if len(userName) == 0 {
		userName = c.GetString(constants.UserName)
	}

	user := &userv1.User{}
	err := h.Cache().Get(ctx, types.NamespacedName{Name: userName}, user)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	// list every namespace the platform owns
	listFunc := listAllManagedNsFunc(ctx)

	// narrow it to the given tenants
	if len(tenantList) > 0 {
		listFunc = listManagedNsByTenantsFunc(ctx, tenantList)
	}

	items := make([]respBody, 0)

	// search in every cluster
	for _, cluster := range clusters {
		cli := cluster.Client
		nsList, err := listFunc(cli) // these namespaces contain project ns and ns under project
		if err != nil {
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
			return
		}

		clusterName := cluster.RawCluster.Annotations[constants.CubeCnAnnotation]
		if len(clusterName) == 0 {
			clusterName = cluster.Name
		}

		for _, ns := range nsList.Items {
			if !ns.ObjectMeta.DeletionTimestamp.IsZero() {
				continue
			}

			// keep only the project's spaces: the project's own namespace is
			// owned by the same project, but it is not a space
			if level, ok := ownership.LevelOf(&ns); !ok || level != ownership.LevelSpace {
				continue
			}

			project, ok := ownership.ProjectOf(&ns)
			if !ok {
				continue
			}

			tenant, ok := ownership.TenantOf(&ns)
			if !ok {
				continue
			}

			if fuzzyName != "" && !strings.Contains(ns.Name, fuzzyName) {
				continue
			}

			// only care about ns under project that the userName can see
			if !transition.UserBelongsToTenant(user, tenant) && !transition.UserBelongsToProject(user, project) {
				continue
			}

			item := respBody{
				Namespace:     ns.Name,
				Cluster:       cluster.Name,
				ClusterName:   clusterName,
				Project:       project,
				Tenant:        tenant,
				NamespaceBody: ns,
			}

			items = append(items, item)
		}

		// A platform administrator also sees the namespaces the platform does not
		// own, marked as unmanaged: they exist, they are simply not part of any
		// tenant. Nobody else is shown them, because they belong to no scope a
		// tenant or project user could be a member of.
		if userv1.IsPlatformAdmin(user) && len(tenantList) == 0 {
			unowned, err := listUnownedNsFunc(ctx)(cli)
			if err != nil {
				response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
				return
			}

			for _, ns := range unowned.Items {
				if !ns.DeletionTimestamp.IsZero() {
					continue
				}
				if fuzzyName != "" && !strings.Contains(ns.Name, fuzzyName) {
					continue
				}

				items = append(items, respBody{
					Namespace:     ns.Name,
					Cluster:       cluster.Name,
					ClusterName:   clusterName,
					Unmanaged:     true,
					NamespaceBody: ns,
				})
			}
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].NamespaceBody.CreationTimestamp.Time.After(items[j].NamespaceBody.CreationTimestamp.Time)
	})

	res := map[string]interface{}{
		"total": len(items),
		"items": items,
	}

	response.SuccessReturn(c, res)
}

// scriptData is the data to render script
type scriptData struct {
	ClusterName string `json:"clusterName"`
	KubeConfig  string `json:"kubeConfig"`
	K8sEndpoint string `json:"k8sEndpoint,omitempty"`
	NetworkType string `json:"networkType,omitempty"`
	Description string `json:"description,omitempty"`
	HarborAddr  string `json:"harborAddr,omitempty"`
}

// addCluster return script which need be execute in member cluster node
// @Summary Add cluster
// @Description add cluster to KubeCube
// @Tags cluster
// @Param scriptData body scriptData true "new cluster raw data"
// @Success 200 {string} string "success"
// @Failure 400 {object} errcode.ErrorInfo
// @Failure 500 {object} errcode.ErrorInfo
// @Router /api/v1/cube/clusters/addCluster  [post]
func (h *handler) addCluster(c *gin.Context) {
	const (
		// The CNI the platform installs by default. It is metadata on the
		// cluster record, not a constraint: a member cluster may run any CNI,
		// and the platform never assumes one.
		defaultNetworkType = "cilium"
		defaultDescription = "this is member cluster"
	)

	d := scriptData{}
	err := c.ShouldBindJSON(&d)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	if len(d.Description) == 0 {
		d.Description = defaultDescription
	}

	if len(d.NetworkType) == 0 {
		d.NetworkType = defaultNetworkType
	}

	kubeConfig, err := base64.StdEncoding.DecodeString(d.KubeConfig)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "kubeConfig invalid: %v", err))
		return
	}

	config, err := kubeconfig.LoadKubeConfigFromBytes(kubeConfig)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "kubeConfig invalid: %v", err))
		return
	}

	if len(d.K8sEndpoint) == 0 {
		d.K8sEndpoint = config.Host
	}

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: d.ClusterName,
		},
		Spec: clusterv1.ClusterSpec{
			KubeConfig:            kubeConfig,
			KubernetesAPIEndpoint: config.Host,
			IsMemberCluster:       true,
			Description:           d.Description,
		},
	}

	if len(d.NetworkType) > 0 {
		cluster.Spec.NetworkType = d.NetworkType
	}

	if len(d.HarborAddr) > 0 {
		cluster.Spec.HarborAddr = d.HarborAddr
	}

	if access := access.AllowAccess(constants.LocalCluster, c.Request, constants.CreateVerb, cluster); !access {
		response.FailReturn(c, errcode.ForbiddenErr)
		return
	}

	err = h.Direct().Create(c.Request.Context(), cluster)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			clog.Warn(err.Error())
		} else {
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
			return
		}
	}

	response.SuccessJsonReturn(c, "success")
}

// registerCluster is a callback api for add cluster to pivot cluster
func (h *handler) registerCluster(c *gin.Context) {
	cluster := &clusterv1.Cluster{}
	err := c.ShouldBindJSON(cluster)
	if err != nil {
		clog.Error(err.Error())
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	err = h.Direct().Create(c.Request.Context(), cluster)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			clog.Warn(err.Error())
		} else {
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
			return
		}
	}

	response.SuccessJsonReturn(c, "success")
}

type nsAndQuota struct {
	Cluster       string                   `json:"cluster"`
	Space         *transition.SpaceRequest `json:"space"`
	ResourceQuota *v1.ResourceQuota        `json:"resourceQuota"`
}

// createNsAndQuota create quota when rbac was spread to new namespace
// @Summary create subNamespace and resourceQuota
// @Description create subNamespace and resourceQuota
// @Tags cluster
// @Param nsAndQuota body nsAndQuota true "ns and quota data"
// @Success 200 {string} string "success"
// @Failure 400 {object} errcode.ErrorInfo
// @Failure 500 {object} errcode.ErrorInfo
// @Router /api/v1/cube/clusters/nsquota  [post]
func (h *handler) createNsAndQuota(c *gin.Context) {
	data := &nsAndQuota{}
	err := c.ShouldBindJSON(data)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	username := c.GetString(constants.UserName)
	cli := clients.Interface().Kubernetes(data.Cluster)
	ctx := c.Request.Context()

	ns := data.Space.Namespace()
	if ns == nil {
		response.FailReturn(c, errcode.InvalidBodyFormat)
		return
	}

	// create namespace directly
	err = cli.Direct().Create(ctx, ns)
	if err != nil {
		response.FailReturn(c, errcode.BadRequest(err))
		return
	}

	rollback := func() {
		err := cli.Direct().Delete(ctx, ns)
		if err != nil {
			clog.Error(err.Error())
		}
	}

	const (
		retryCount    = 20
		retryInterval = 100 * time.Millisecond
	)

	_, clusterRoles, err := h.Interface.RolesFor(&userinfo.DefaultInfo{Name: username}, "")
	if err != nil {
		rollback()
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	// platform level clusterRole does not need wait rbac spread
	toWait := true
	for _, r := range clusterRoles {
		if v, ok := r.GetLabels()[constants.RoleLabel]; ok {
			if v == "platform" {
				toWait = false
			}
		}
	}

	// wait for rbac resources spread
	count := 0
	for toWait {
		if count == retryCount {
			clog.Warn("the rolebindings did not reach namespace %s within %v retries", data.Space.Name, retryCount)
			break
		}

		list := &rbacv1.RoleBindingList{}
		err = cli.Direct().List(ctx, list, &client.ListOptions{Namespace: data.Space.Name})
		if err != nil {
			rollback()
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
			return
		}
		if len(list.Items) > 0 {
			break
		}
		count++
		time.Sleep(retryInterval)
	}

	// final action failed would rollback whole action
	err = cli.Direct().Create(ctx, data.ResourceQuota)
	if err != nil {
		rollback()
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	clog.Debug("user %v create ns %v and resourceQuota %v in cluster %v success",
		username, data.Space.Name, data.ResourceQuota.Name, data.Cluster)

	response.SuccessJsonReturn(c, "success")
}

// onboardNamespace takes a namespace the platform does not own under
// management: it labels it as a space of the project the request names and gives
// it the ResourceQuota the console expects a space to have.
//
// The body is createNsAndQuota's, because the console builds one payload shape
// for both: the namespace to adopt is named where a created one would be, and
// the tenant and project are its labels. What differs is that the namespace is
// already there and holds whatever it holds, so nothing is rolled back here —
// see the quota step.
//
// @Summary onboard an unmanaged namespace as a space
// @Description label an existing unmanaged namespace as a space of a project and create its resourceQuota
// @Tags cluster
// @Param nsAndQuota body nsAndQuota true "namespace and quota data"
// @Success 200 {string} string "success"
// @Failure 400 {object} errcode.ErrorInfo
// @Failure 500 {object} errcode.ErrorInfo
// @Router /api/v1/cube/clusters/nsquota/onboard  [post]
func (h *handler) onboardNamespace(c *gin.Context) {
	data := &nsAndQuota{}
	err := c.ShouldBindJSON(data)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	ctx := c.Request.Context()
	username := c.GetString(constants.UserName)

	// Onboarding hands the namespace and everything already in it to the
	// project's members. Creating a space only hands out a new one, which is why
	// that path has no gate; this one changes who can reach data that exists, so
	// it is a platform administrator's decision.
	user := &userv1.User{}
	err = h.Cache().Get(ctx, types.NamespacedName{Name: username}, user)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}
	if !userv1.IsPlatformAdmin(user) {
		response.FailReturn(c, errcode.ForbiddenErr)
		return
	}

	if data.Space == nil || data.ResourceQuota == nil {
		response.FailReturn(c, errcode.InvalidBodyFormat)
		return
	}

	labels, ok := data.Space.OwnershipLabels()
	if !ok {
		response.FailReturn(c, errcode.InvalidBodyFormat)
		return
	}
	tenant := data.Space.Labels[constants.TenantLabel]
	project := data.Space.Labels[constants.ProjectLabel]

	cli := clients.Interface().Kubernetes(data.Cluster)
	if cli == nil {
		response.FailReturn(c, errcode.ClusterNotFoundError(data.Cluster))
		return
	}

	ns := &v1.Namespace{}
	err = cli.Direct().Get(ctx, types.NamespacedName{Name: data.Space.Name}, ns)
	if err != nil {
		if errors.IsNotFound(err) {
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "namespace %v does not exist in cluster %v", data.Space.Name, data.Cluster))
			return
		}
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	if err = onboardable(ns, tenant, project); err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "namespace %v cannot be onboarded: %v", ns.Name, err))
		return
	}

	// The target has to exist before the labels are written. An owner label
	// naming a project that is not there would leave the namespace managed by
	// nothing, which is worse than leaving it unmanaged and visible as such.
	projectCR := &tenantv1.Project{}
	err = h.Cache().Get(ctx, types.NamespacedName{Name: project}, projectCR)
	if err != nil {
		if errors.IsNotFound(err) {
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "project %v does not exist", project))
			return
		}
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	// The project says which tenant it sits in, and that is the tenant a space of
	// it belongs to. Writing the tenant the request asks for instead would put
	// the namespace in one tenant while its project belongs to another, and a
	// tenant-scoped reader resolves a namespace through this key.
	if projectTenant := projectCR.Labels[constants.TenantLabel]; projectTenant != tenant {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "project %v belongs to tenant %v, not %v", project, projectTenant, tenant))
		return
	}

	tenantCR := &tenantv1.Tenant{}
	err = h.Cache().Get(ctx, types.NamespacedName{Name: tenant}, tenantCR)
	if err != nil {
		if errors.IsNotFound(err) {
			response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "tenant %v does not exist", tenant))
			return
		}
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	// The quota has to land in the namespace being onboarded, whatever the body
	// says: a quota in another namespace would leave this one unadmitted and
	// create a quota where nobody asked for one.
	if data.ResourceQuota.Name == "" || data.ResourceQuota.Namespace != ns.Name {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, "the resourceQuota must be named and created in namespace %v", ns.Name))
		return
	}

	// Everything that can be known before the namespace changes is known here, so
	// that the two writes that follow are the only things left to fail. A quota
	// already under the name this space is supposed to use means a previous
	// attempt got this far; the namespace is still unmanaged, so the leftover can
	// be looked at and removed before trying again.
	leftover := &v1.ResourceQuota{}
	err = cli.Direct().Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: data.ResourceQuota.Name}, leftover)
	if err == nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest,
			"namespace %v already holds a resourceQuota named %v", ns.Name, data.ResourceQuota.Name))
		return
	}
	if !errors.IsNotFound(err) {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	// The labels are the commit point: every reader of a namespace's tenancy goes
	// through them, and from here on the namespace is a space of the project —
	// its members reach it, the quota webhook admits it under the project's
	// chain, and deleting the project deletes it.
	//
	// They are written before the quota because the webhook selects on the owner
	// label: a quota created first would be admitted without ever being checked
	// against the project's quota chain, which is the check that keeps a space
	// from being given more than its project has.
	patch := client.MergeFrom(ns.DeepCopy())
	if ns.Labels == nil {
		ns.Labels = make(map[string]string)
	}
	for k, v := range labels {
		ns.Labels[k] = v
	}
	err = cli.Direct().Patch(ctx, ns, patch)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	err = cli.Direct().Create(ctx, data.ResourceQuota)
	if err != nil {
		// There is no rollback: the namespace is managed from here on, and undoing
		// that by deleting it would take everything it holds with it. The failure
		// is reported as the state it leaves behind, so an administrator can see
		// that the space exists without its quota rather than that nothing
		// happened.
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest,
			"namespace %v is now managed by project %v, but its resourceQuota was not created: %v", ns.Name, project, err))
		return
	}

	clog.Info("user %v onboarded namespace %v into project %v of tenant %v in cluster %v",
		username, ns.Name, project, tenant, data.Cluster)

	response.SuccessJsonReturn(c, "success")
}

type getCubeResourceQuotaResp struct {
	Total int                     `json:"total"`
	Items []cubeResourceQuotaData `json:"items"`
}

type cubeResourceQuotaData struct {
	ClusterName       string                     `json:"clusterName"`
	ClusterIdentity   string                     `json:"clusterIdentity"`
	Tenant            string                     `json:"tenant"`
	TenantName        string                     `json:"tenantName"`
	CubeResourceQuota *quotav1.CubeResourceQuota `json:"cubeResourceQuota"`
	ExclusiveNodeHard map[string]v1.ResourceList `json:"exclusiveNodeHard"`
	ClusterState      *clusterv1.ClusterState    `json:"clusterState"`
}

func (h *handler) getCubeResourceQuota(c *gin.Context) {
	ts := c.Query("tenants")
	cs := c.Query("clusters")
	userName := c.Query("user")
	ctx := c.Request.Context()

	tenants, clusters := strings.Split(ts, ";"), strings.Split(cs, ";")

	if len(userName) == 0 {
		userName = c.GetString(constants.UserName)
	}

	if len(cs) == 0 {
		clusters = multicluster.Interface().ListClustersNameByType(multicluster.AllCluster)
	}

	if len(ts) == 0 {
		tenants = nil
	}

	visibleTenants, visibleTenantsCr, err := getVisibleTenants(ctx, h.Client, userName, tenants)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	res, err := listCubeResourceQuota(ctx, h.Client, visibleTenants, visibleTenantsCr, clusters)
	if err != nil {
		response.FailReturn(c, errcode.CustomReturn(http.StatusBadRequest, err.Error()))
		return
	}

	res = sortCubeResourceQuotas(res)

	response.SuccessReturn(c, getCubeResourceQuotaResp{Total: len(res), Items: res})
}
