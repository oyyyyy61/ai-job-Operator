# 使用指南

## 最小 AIJob

```yaml
apiVersion: ai.gpustack.io/v1alpha1
kind: AIJob
metadata:
  name: cuda-check
  namespace: default
spec:
  image: nvidia/cuda:12.4.1-base-ubuntu22.04
  command: ["/bin/bash", "-c"]
  args: ["nvidia-smi && sleep 20"]
  gpu: 1
  priority: normal
  queue: default
  runtimeClassName: nvidia
```

```bash
kubectl apply -f aijob.yaml
kubectl get aijob,pod,podgroup -w
```

## 字段

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `image` | string | 必填 | worker 容器镜像 |
| `command` | string[] | 镜像默认值 | 容器 entrypoint |
| `args` | string[] | 镜像默认值 | 容器参数 |
| `env` | name/value[] | 空 | 普通环境变量，不支持 Secret 引用 |
| `gpu` | int32 | `1` | 整卡 NVIDIA GPU 数量，范围 1-8 |
| `priority` | string | `normal` | `low`、`normal`、`high` |
| `queue` | string | `default` | Volcano Queue 名称 |
| `resources` | ResourceRequirements | 空 | CPU、内存等非 GPU request/limit |
| `nodeSelector` | map | 空 | 节点标签约束 |
| `tolerations` | Toleration[] | 空 | 节点污点容忍 |
| `runtimeClassName` | string | 空 | 可选 RuntimeClass，例如 `nvidia` |
| `restartPolicy` | string | `Never` | `Never` 或 `OnFailure` |
| `ownership` | object | 空 | owner、team、project 可观测性标签 |

`spec` 创建后不可修改。需要更换镜像、GPU 或参数时，应删除旧 AIJob 并使用新名称提交。

## CPU 和内存

```yaml
spec:
  resources:
    requests:
      cpu: 500m
      memory: 1Gi
    limits:
      cpu: "2"
      memory: 4Gi
```

GPU 字段统一通过 `spec.gpu` 配置。Controller 会移除 `resources` 中手工填写的
`nvidia.com/gpu`，随后由 Webhook 按 `spec.gpu` 注入 request 和 limit。

## Ownership 标签

```yaml
spec:
  ownership:
    owner: alice
    team: ml-platform
    project: image-training
```

这些字段会转换为：

```text
observability.gpustack.io/owner
observability.gpustack.io/team
observability.gpustack.io/project
observability.gpustack.io/task
```

可观测性系统可以使用这些标签统计不同用户、团队和项目的 GPU 使用情况。

## 状态

```bash
kubectl get aijob
kubectl describe aijob <name>
kubectl get aijob <name> -o yaml
```

| Phase | 含义 |
|---|---|
| `Pending` | Pod 等待调度、拉取镜像或启动 |
| `Running` | worker 容器正在运行 |
| `Succeeded` | Pod 成功结束 |
| `Failed` | Pod 失败结束 |

`status.message` 会优先显示 Pod 的调度失败消息。`status.nodeName` 表示实际运行节点。

## 查看 Webhook 注入结果

```bash
kubectl get pod <aijob-name> \
  -o jsonpath='{.spec.schedulerName}{"\n"}{.spec.priorityClassName}{"\n"}{.spec.priority}{"\n"}{.spec.containers[0].resources}{"\n"}'
```

一个 high、1 GPU 任务应显示：

```text
volcano
ai-high
10000
{"limits":{"nvidia.com/gpu":"1"},"requests":{"nvidia.com/gpu":"1"}}
```

## 优先级

项目定义：

| AIJob priority | PriorityClass | 数值 |
|---|---|---:|
| `low` | `ai-low` | 100 |
| `normal` | `ai-normal` | 1000 |
| `high` | `ai-high` | 10000 |

当前 Volcano scheduler 启用了 `priority` plugin。多个 Pending 任务竞争同一 GPU 时，high
会排在 normal 和 low 前面。当前默认 actions 没有启用 `preempt`，因此 high 不会驱逐已经
运行的 low 任务。

两张 GPU 的优先级演示：

```bash
kubectl apply -f config/samples/gpu-blockers.yaml
kubectl wait --for=jsonpath='{.status.phase}'=Running \
  aijob/gpu-blocker-a aijob/gpu-blocker-b --timeout=10m

kubectl apply -f config/samples/priority-demo.yaml
kubectl get aijob,pod,podgroup -w
```

此时 low 和 high 都应 Pending。释放一张 GPU：

```bash
kubectl delete aijob gpu-blocker-a
```

预期 `priority-demo-high` 先进入 Running。实验结束后清理：

```bash
kubectl delete -f config/samples/priority-demo.yaml --ignore-not-found
kubectl delete -f config/samples/gpu-blockers.yaml --ignore-not-found
```

## Queue

默认使用 Volcano `default` Queue：

```bash
kubectl get queue
kubectl get queue default -o yaml
```

使用自定义 Queue 前，需要先在 Volcano 中创建对应 Queue，再在 AIJob 中设置：

```yaml
spec:
  queue: research-team
```

## 删除

```bash
kubectl delete aijob <name>
```

Pod 和 PodGroup 都带有 AIJob owner reference，Kubernetes garbage collector 会级联删除。
