#!/usr/bin/env bash
set -euo pipefail

VOLCANO_REF=${VOLCANO_REF:-2c37d5340035f621222c45dd26667c625fc7c637}
VOLCANO_MANIFEST=${VOLCANO_MANIFEST:-}
EXPECTED_API_SERVERS=${EXPECTED_API_SERVERS:-}

for required_command in kubectl; do
  if ! command -v "$required_command" >/dev/null 2>&1; then
    echo "Required command is missing: $required_command" >&2
    exit 1
  fi
done

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

if [[ -n "$VOLCANO_MANIFEST" ]]; then
  manifest=$VOLCANO_MANIFEST
else
  manifest="https://raw.githubusercontent.com/volcano-sh/volcano/${VOLCANO_REF}/installer/volcano-development.yaml"
fi

if kubectl get crd podgroups.scheduling.volcano.sh >/dev/null 2>&1 \
  && kubectl -n volcano-system get deployment volcano-scheduler volcano-controllers volcano-admission >/dev/null 2>&1; then
  echo "Volcano resources already exist; checking rollout status."
else
  echo "Installing Volcano from ${manifest}"
  kubectl apply -f "$manifest"
fi

kubectl wait --for=condition=Established crd/podgroups.scheduling.volcano.sh --timeout=3m
kubectl -n volcano-system rollout status deployment/volcano-scheduler --timeout=5m
kubectl -n volcano-system rollout status deployment/volcano-controllers --timeout=5m
kubectl -n volcano-system rollout status deployment/volcano-admission --timeout=5m

scheduler_config=$(kubectl -n volcano-system get configmap volcano-scheduler-configmap -o jsonpath='{.data.volcano-scheduler\.conf}')
if [[ "$scheduler_config" != *"name: priority"* ]]; then
  echo "Volcano scheduler configuration does not enable the priority plugin." >&2
  exit 1
fi

echo "Volcano is ready and its priority plugin is enabled."
