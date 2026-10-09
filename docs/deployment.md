# 部署指南

## 1. 前置条件

部署前需要确认：

```bash
kubectl config current-context
kubectl cluster-info
kubectl get nodes
kubectl get nodes -o custom-columns='NAME:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].status,GPU:.status.allocatable.nvidia\.com/gpu'
kubectl get runtimeclass
```

至少一个 Ready 节点应提供 `nvidia.com/gpu`。如果样例设置了
`runtimeClassName: nvidia`，集群中还需要存在同名 RuntimeClass。

本地构建工具：

```bash
go version
kubectl version --client
kustomize version
controller-gen --version
docker version
openssl version
```

## 2. 安装 Volcano

```bash
bash scripts/install-volcano.sh
```

默认 `VOLCANO_REF` 固定为项目验证过的 commit。覆盖方式：

```bash
VOLCANO_REF=<tag-or-commit> bash scripts/install-volcano.sh
VOLCANO_MANIFEST=/absolute/path/volcano.yaml bash scripts/install-volcano.sh
```

对重要集群增加 API Server 白名单：

```bash
EXPECTED_API_SERVERS=https://api.example.com:6443 bash scripts/install-volcano.sh
```

脚本会等待 PodGroup CRD 建立，并检查 scheduler、controllers、admission 三个 Deployment
以及 Volcano `priority` plugin。

## 3. 构建和推送镜像

```bash
export IMG=registry.example.com/platform/aijob-operator:v0.1.0
make docker-build IMG="$IMG"
make docker-push IMG="$IMG"
```

使用国内 Go module 代理：

```bash
make docker-build IMG="$IMG" GOPROXY=https://goproxy.cn,direct
```

所有可能运行 Operator 的节点都需要能够解析、连接和认证这个镜像地址。推荐使用带 TLS
和认证的内部 Registry。

### HTTP Registry

containerd 默认会把普通 Registry 地址按 HTTPS 处理。使用 HTTP Registry 时，必须在每个
候选节点配置 containerd 或 RKE2 Registry mirror，并重启对应节点服务。节点未配置时，
常见错误为：

```text
http: server gave HTTP response to HTTPS client
```

生产环境优先为 Registry 配置 TLS。RKE2 集群应按照 RKE2 Private Registry Configuration
文档维护 `/etc/rancher/rke2/registries.yaml`。

## 4. 部署 Operator

通用部署：

```bash
IMAGE="$IMG" bash scripts/deploy.sh
```

带目标保护和 GPU 节点预检的部署：

```bash
EXPECTED_API_SERVERS=https://192.168.50.11:6443,https://192.168.50.12:6443 \
EXPECTED_NODES=gpu-node-14,gpu-node-15,gpustack-cp01,gpustack-cp02 \
GPU_NODES=gpu-node-14,gpu-node-15 \
MIN_GPU_PER_NODE=1 \
IMAGE="$IMG" \
bash scripts/deploy.sh
```

参数说明：

| 参数 | 必填 | 说明 |
|---|---|---|
| `IMAGE` | 是 | 所有候选节点可拉取的 Operator 镜像 |
| `EXPECTED_API_SERVERS` | 否 | 逗号分隔的允许 API Server 地址 |
| `EXPECTED_NODES` | 否 | 部署前必须存在的节点名称 |
| `GPU_NODES` | 否 | 需要验证 GPU capacity 的节点名称 |
| `MIN_GPU_PER_NODE` | 否 | 每个 GPU 节点的最小整卡数量，默认 1 |

部署脚本执行内容：

1. 检查命令、镜像参数和当前 API Server。
2. 检查可选节点列表和 GPU capacity。
3. 检查 Volcano PodGroup CRD 和 scheduler。
4. 安装三档 PriorityClass。
5. 创建 `aijob-operator-system` namespace。
6. 生成有效期 365 天的 Webhook 自签名证书。
7. 安装 CRD、RBAC、Manager、Webhook 和 Service。
8. 设置 Operator 镜像并滚动更新。
9. 写入 MutatingWebhookConfiguration CA bundle。
10. 等待 Deployment Ready 并显示 EndpointSlice。

## 5. 验证

```bash
bash scripts/verify.sh
```

验证范围包括：

- AIJob 和 PodGroup CRD。
- 三档 PriorityClass。
- Volcano scheduler。
- Operator Deployment。
- Webhook Service、EndpointSlice 和 CA bundle。
- AIJob 服务端 schema 校验。
- Webhook 对测试 Pod 的 GPU、scheduler 和 priority 注入。

查看运行状态：

```bash
kubectl -n aijob-operator-system get deployment,pod,service,endpointslice
kubectl -n aijob-operator-system logs deployment/aijob-operator-controller-manager
kubectl -n volcano-system get deployment,pod
```

## 6. 升级镜像

使用新标签构建、推送并重新运行部署脚本：

```bash
export IMG=registry.example.com/platform/aijob-operator:v0.1.1
make docker-build IMG="$IMG"
make docker-push IMG="$IMG"
IMAGE="$IMG" bash scripts/deploy.sh
bash scripts/verify.sh
```

部署脚本会重新生成 Webhook 证书。持续运行环境建议接入 cert-manager 或证书轮换控制器。

## 7. 卸载

先清理示例任务：

```bash
kubectl delete -f config/samples/ai_v1alpha1_aijob.yaml --ignore-not-found
kubectl delete -f config/samples/priority-demo.yaml --ignore-not-found
kubectl delete -f config/samples/gpu-blockers.yaml --ignore-not-found
```

卸载 Operator 和 PriorityClass：

```bash
kubectl delete -k config/default --ignore-not-found
kubectl delete -k config/scheduler --ignore-not-found
```

Volcano 可能同时服务其他批处理任务。卸载前应确认没有其他依赖，再按照实际安装清单清理。
