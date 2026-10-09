#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
PROJECT_DIR=$(cd -- "$SCRIPT_DIR/.." && pwd)
IMAGE=${IMAGE:-}
EXPECTED_API_SERVERS=${EXPECTED_API_SERVERS:-}
EXPECTED_NODES=${EXPECTED_NODES:-}
GPU_NODES=${GPU_NODES:-}
MIN_GPU_PER_NODE=${MIN_GPU_PER_NODE:-1}
NAMESPACE=aijob-operator-system
WEBHOOK_SERVICE=aijob-operator-webhook-service
WEBHOOK_CONFIGURATION=aijob-operator-mutating-webhook-configuration

for required_command in kubectl kustomize openssl base64; do
  if ! command -v "$required_command" >/dev/null 2>&1; then
    echo "Required command is missing: $required_command" >&2
    exit 1
  fi
done

if [[ -z "$IMAGE" || "$IMAGE" == "controller:latest" ]]; then
  echo "Set IMAGE to an image reachable by every cluster node." >&2
  echo "Example: IMAGE=registry.local/aijob-operator:v0.1.0 ./scripts/deploy.sh" >&2
  exit 1
fi
if [[ ! "$MIN_GPU_PER_NODE" =~ ^[0-9]+$ ]] || (( MIN_GPU_PER_NODE < 1 )); then
  echo "MIN_GPU_PER_NODE must be a positive integer." >&2
  exit 1
fi

api_server=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
echo "Using Kubernetes API server: ${api_server}"
if [[ -n "$EXPECTED_API_SERVERS" ]]; then
  case ",${EXPECTED_API_SERVERS}," in
    *",${api_server},"*) ;;
    *)
      echo "Active API server ${api_server} is outside EXPECTED_API_SERVERS=${EXPECTED_API_SERVERS}." >&2
      exit 1
      ;;
  esac
fi

IFS=',' read -r -a expected_nodes <<< "$EXPECTED_NODES"
for expected_node in "${expected_nodes[@]}"; do
  [[ -z "$expected_node" ]] || kubectl get node "$expected_node" >/dev/null
done
IFS=',' read -r -a gpu_nodes <<< "$GPU_NODES"
for gpu_node in "${gpu_nodes[@]}"; do
  [[ -z "$gpu_node" ]] && continue
  gpu_capacity=$(kubectl get node "$gpu_node" -o jsonpath='{.status.capacity.nvidia\.com/gpu}')
  if [[ ! "$gpu_capacity" =~ ^[0-9]+$ ]] || (( gpu_capacity < MIN_GPU_PER_NODE )); then
    echo "Expected ${gpu_node} to expose at least ${MIN_GPU_PER_NODE} nvidia.com/gpu; found ${gpu_capacity:-none}." >&2
    exit 1
  fi
done

kubectl get crd podgroups.scheduling.volcano.sh >/dev/null
kubectl -n volcano-system get deployment volcano-scheduler >/dev/null

kubectl apply -k "$PROJECT_DIR/config/scheduler"
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

certificate_dir=$(mktemp -d)
trap 'rm -rf -- "$certificate_dir"' EXIT
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout "$certificate_dir/tls.key" \
  -out "$certificate_dir/tls.crt" \
  -subj "/CN=${WEBHOOK_SERVICE}.${NAMESPACE}.svc" \
  -addext "subjectAltName=DNS:${WEBHOOK_SERVICE}.${NAMESPACE}.svc,DNS:${WEBHOOK_SERVICE}.${NAMESPACE}.svc.cluster.local" \
  >/dev/null 2>&1

kubectl -n "$NAMESPACE" create secret tls webhook-server-cert \
  --cert="$certificate_dir/tls.crt" \
  --key="$certificate_dir/tls.key" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -k "$PROJECT_DIR/config/default"
kubectl -n "$NAMESPACE" set image deployment/aijob-operator-controller-manager manager="$IMAGE"
kubectl -n "$NAMESPACE" rollout restart deployment/aijob-operator-controller-manager

ca_bundle=$(base64 -w0 < "$certificate_dir/tls.crt")
kubectl patch mutatingwebhookconfiguration "$WEBHOOK_CONFIGURATION" --type=json \
  -p="[{\"op\":\"add\",\"path\":\"/webhooks/0/clientConfig/caBundle\",\"value\":\"${ca_bundle}\"}]"

kubectl -n "$NAMESPACE" rollout status deployment/aijob-operator-controller-manager --timeout=5m
kubectl -n "$NAMESPACE" get endpointslice \
  -l "kubernetes.io/service-name=${WEBHOOK_SERVICE}"

echo "AIJob Operator is ready. Run: ${SCRIPT_DIR}/verify.sh"
