# 排障指南

## 快速检查

```bash
./scripts/verify.sh

kubectl get crd aijobs.ai.gpustack.io podgroups.scheduling.volcano.sh
kubectl -n aijob-operator-system get deployment,pod,service,endpointslice
kubectl -n volcano-system get deployment,pod
kubectl get priorityclass ai-low ai-normal ai-high
```

## Operator Pod 为 ImagePullBackOff

查看事件：

```bash
kubectl -n aijob-operator-system describe pod \
  -l control-plane=controller-manager
```

常见原因：

- 镜像没有推送成功。
- 节点无法解析或连接 Registry。
- 私有仓库凭据缺失。
- Registry 使用 HTTP，containerd 按 HTTPS 连接。
- 镜像架构与节点架构不一致。

出现以下错误时，需要为每个 RKE2 节点配置 Registry mirror，或为仓库启用 TLS：

```text
http: server gave HTTP response to HTTPS client
```

确认镜像引用：

```bash
kubectl -n aijob-operator-system get deployment \
  aijob-operator-controller-manager \
  -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
```

## Webhook 调用失败

```bash
kubectl get mutatingwebhookconfiguration \
  aijob-operator-mutating-webhook-configuration -o yaml
kubectl -n aijob-operator-system get secret webhook-server-cert
kubectl -n aijob-operator-system get endpointslice \
  -l kubernetes.io/service-name=aijob-operator-webhook-service
kubectl -n aijob-operator-system logs \
  deployment/aijob-operator-controller-manager
```

检查点：

- EndpointSlice 中存在 Pod IP 和 `9443` 端口。
- MutatingWebhookConfiguration 中 `caBundle` 非空。
- Secret 包含 `tls.crt` 和 `tls.key`。
- 证书 SAN 包含 Webhook Service DNS 名称。
- Operator Pod Ready 且没有证书加载错误。

重新运行部署脚本可以重新生成证书并更新 CA：

```bash
IMAGE=<current-image> ./scripts/deploy.sh
```

## AIJob 一直 Pending

```bash
kubectl describe aijob <name>
kubectl describe pod <name>
kubectl get podgroup <name> -o yaml
kubectl get events --sort-by=.lastTimestamp | tail -n 50
kubectl -n volcano-system logs deployment/volcano-scheduler --tail=200
```

重点检查：

1. GPU 是否可分配：

   ```bash
   kubectl get nodes \
     -o custom-columns='NAME:.metadata.name,GPU:.status.allocatable.nvidia\.com/gpu'
   ```

2. Pod 是否已经注入 GPU 和 Volcano：

   ```bash
   kubectl get pod <name> \
     -o jsonpath='{.spec.schedulerName}{"\n"}{.spec.priorityClassName}{"\n"}{.spec.containers[0].resources}{"\n"}'
   ```

3. Queue 是否存在且为 Open：

   ```bash
   kubectl get queue
   kubectl get queue <queue-name> -o yaml
   ```

4. `nodeSelector`、tolerations 和 RuntimeClass 是否匹配 GPU 节点。
5. PodGroup `minResources` 是否超过集群剩余资源。
6. GPU 是否被其他 Pod 或宿主机进程使用。

## AIJob 存在，Pod 没有创建

```bash
kubectl describe aijob <name>
kubectl -n aijob-operator-system logs \
  deployment/aijob-operator-controller-manager --since=10m
kubectl get podgroup <name> -o yaml
```

可能原因：

- 同名 Pod 或 PodGroup 已存在，且 owner reference 不属于当前 AIJob。
- Volcano PodGroup CRD 不可用。
- Webhook 拒绝 Pod，例如 GPU 或 priority 注解非法。
- Controller ServiceAccount 权限被修改。

## 优先级没有达到预期

```bash
kubectl get pod <name> \
  -o jsonpath='{.spec.schedulerName}{" "}{.spec.priorityClassName}{" "}{.spec.priority}{"\n"}'
kubectl -n volcano-system get configmap volcano-scheduler-configmap \
  -o jsonpath='{.data.volcano-scheduler\.conf}'
```

确认 scheduler 为 `volcano`，PriorityClass 和整数优先级一致，并且 Volcano 配置包含
`priority` plugin。

优先级默认影响等待队列顺序。需要主动驱逐低优先级运行任务时，还要审查并启用 Volcano
`preempt` action；启用前应评估训练任务中断和数据保存策略。

## 清理卡住的演示资源

```bash
kubectl delete -f config/samples/ai_v1alpha1_aijob.yaml --ignore-not-found
kubectl delete -f config/samples/priority-demo.yaml --ignore-not-found
kubectl delete -f config/samples/gpu-blockers.yaml --ignore-not-found
kubectl get aijob,pod,podgroup -A
```
