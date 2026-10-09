#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
PROJECT_DIR=$(cd -- "$SCRIPT_DIR/.." && pwd)
NAMESPACE=aijob-operator-system

kubectl get crd aijobs.ai.gpustack.io podgroups.scheduling.volcano.sh
kubectl get priorityclass ai-low ai-normal ai-high
kubectl -n volcano-system get deployment volcano-scheduler
kubectl -n "$NAMESPACE" get deployment aijob-operator-controller-manager
kubectl -n "$NAMESPACE" get service aijob-operator-webhook-service
kubectl -n "$NAMESPACE" get endpointslice \
  -l kubernetes.io/service-name=aijob-operator-webhook-service

ca_bundle=$(kubectl get mutatingwebhookconfiguration aijob-operator-mutating-webhook-configuration \
  -o jsonpath='{.webhooks[0].clientConfig.caBundle}')
if [[ -z "$ca_bundle" ]]; then
  echo "MutatingWebhookConfiguration has no CA bundle." >&2
  exit 1
fi

kubectl apply --dry-run=server -f "$PROJECT_DIR/config/samples/ai_v1alpha1_aijob.yaml" >/dev/null

mutation=$(kubectl apply --dry-run=server -f - -o jsonpath='{.spec.schedulerName},{.spec.priorityClassName},{.spec.priority},{.spec.containers[?(@.name=="worker")].resources.requests.nvidia\.com/gpu},{.spec.containers[?(@.name=="worker")].resources.limits.nvidia\.com/gpu}' <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: aijob-webhook-probe
  namespace: default
  labels:
    ai.gpustack.io/managed: "true"
  annotations:
    ai.gpustack.io/gpu-count: "1"
    ai.gpustack.io/priority: high
spec:
  restartPolicy: Never
  containers:
  - name: worker
    image: busybox:1.36
    command: ["true"]
EOF
)
if [[ "$mutation" != "volcano,ai-high,10000,1,1" ]]; then
  echo "Unexpected webhook mutation result: ${mutation}" >&2
  exit 1
fi

echo "CRD validation, Volcano, controller, webhook service, CA bundle, and Pod mutation checks passed."
