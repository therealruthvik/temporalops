#!/usr/bin/env bash
# Install Kyverno into the kind cluster and apply the image policy. Idempotent.
set -euo pipefail

KYVERNO_VERSION="${KYVERNO_VERSION:-v1.13.4}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "installing Kyverno ${KYVERNO_VERSION}"
# Server-side apply: Kyverno's CRDs carry annotations larger than the 256KB
# client-side apply limit, so a plain `kubectl apply` fails with
# "metadata.annotations: Too long".
kubectl apply --server-side --force-conflicts \
  -f "https://github.com/kyverno/kyverno/releases/download/${KYVERNO_VERSION}/install.yaml"

echo "waiting for Kyverno admission controller to be ready"
kubectl -n kyverno rollout status deployment/kyverno-admission-controller --timeout=180s

echo "applying image policy"
kubectl apply -f "${REPO_ROOT}/deploy/kyverno/require-approved-image.yaml"

echo
echo "policy active:"
kubectl get clusterpolicy require-approved-image
echo
echo "approved images (nginx:*) are admitted; anything else is denied."
