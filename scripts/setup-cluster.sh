#!/usr/bin/env bash
# Create (or reuse) a local kind cluster and deploy the sample app that the
# CanaryDeployWorkflow operates on. Idempotent: safe to run repeatedly.
set -euo pipefail

CLUSTER="${CLUSTER:-temporalops}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if kind get clusters 2>/dev/null | grep -qx "${CLUSTER}"; then
  echo "kind cluster '${CLUSTER}' already exists, reusing"
else
  echo "creating kind cluster '${CLUSTER}'"
  kind create cluster --name "${CLUSTER}"
fi

# kubectl context kind creates is named kind-<cluster>.
kubectl config use-context "kind-${CLUSTER}" >/dev/null

echo "applying sample app"
kubectl apply -f "${REPO_ROOT}/deploy/k8s/namespace.yaml"
kubectl apply -f "${REPO_ROOT}/deploy/k8s/sample-app.yaml"

echo "waiting for stable rollout"
kubectl -n temporalops rollout status deployment/web-stable --timeout=120s

echo
echo "ready. current state:"
kubectl -n temporalops get deploy,svc -l app=web
echo
echo "the worker uses your current kubecontext (kind-${CLUSTER}); run 'make worker'."
