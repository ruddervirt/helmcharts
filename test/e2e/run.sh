#!/usr/bin/env bash
# End-to-end test on kind.
#
# Multus CRDs:
# 1. Apply the upstream NetworkAttachmentDefinition CRD of release N with
#    "kubectl apply" (as ruddervirt-setup did) and create a NetworkAttachmentDefinition.
# 2. Adopt the CRD with "helm install --take-ownership" at N. Make sure that the CRD
#    spec did not change and the NetworkAttachmentDefinition still exists.
# 3. Upgrade with Helm to N+1 (the chart under test).
# 4. Run "helm uninstall". Make sure that the CRD and the NetworkAttachmentDefinition stay.
#
# KubeVirt and CDI:
# 1. Install upstream KubeVirt and CDI at version N with "kubectl apply" (as ruddervirt-setup does).
# 2. Patch feature gates into the KubeVirt CR (as Aileron does).
# 3. Adopt both installs with "helm install --take-ownership" at version N.
#    Make sure that no pod restarted and the feature gates stayed.
#    KubeVirt gets no uninstallStrategy yet, because that change rolls all components.
# 4. Upgrade with Helm to version N+1 (the chart under test).
#    Make sure that the operators report N+1, the feature gates stayed, and
#    KubeVirt now has uninstallStrategy: BlockUninstallIfWorkloadsExist.
# 5. Run "helm uninstall". Make sure that the CRs, the CRDs, the operators, the
#    components, and a VirtualMachine stay in place.
#
# N is the stable upstream release before the chart under test.
#
# Environment:
#   KUBEVIRT_CHART, CDI_CHART, MULTUS_CRDS_CHART
#                              chart directories under test (default: newest in charts/)
#   KIND_CLUSTER               kind cluster name (default: chart-e2e)
#   E2E_REUSE_CLUSTER=1        use an existing cluster with that name
#   E2E_KEEP_CLUSTER=1         do not delete the cluster at the end
#   KIND, HELM, KUBECTL        binaries (default: from PATH)
#
# The script uses its own kubeconfig file. It does not change the current kubectl context.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"

KIND=${KIND:-kind}
HELM=${HELM:-helm}
KUBECTL=${KUBECTL:-kubectl}
CLUSTER=${KIND_CLUSTER:-chart-e2e}
KV_NS=kubevirt
CDI_NS=cdi
WORK=$(mktemp -d)
# Use a private kubeconfig, so the test never touches the current context.
export KUBECONFIG="$WORK/kubeconfig"
CHARTGEN=(go run ./cmd/chartgen)

log() { printf '\n=== %s\n' "$*"; }
fail() { printf '\nFAIL: %s\n' "$*" >&2; exit 1; }

cleanup() {
  local rc=$?
  if [[ $rc -ne 0 ]]; then
    echo "--- diagnostics"
    $KUBECTL get pods -A -o wide || true
    $KUBECTL get kubevirt -A -o yaml | grep -A30 '^  status:' || true
    $KUBECTL get cdi -o yaml | grep -A30 '^  status:' || true
  fi
  if [[ "${E2E_KEEP_CLUSTER:-}" == 1 ]]; then
    echo "cluster kept. kubeconfig: $KIND get kubeconfig --name $CLUSTER"
  fi
  if [[ "${E2E_KEEP_CLUSTER:-}" != 1 && "${E2E_REUSE_CLUSTER:-}" != 1 ]]; then
    $KIND delete cluster --name "$CLUSTER" --kubeconfig "$WORK/kubeconfig" >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
  exit $rc
}
trap cleanup EXIT

appversion() { sed -n 's/^appVersion: //p' "$1/Chart.yaml"; }

KUBEVIRT_CHART=${KUBEVIRT_CHART:-$("${CHARTGEN[@]}" latest --project kubevirt)}
CDI_CHART=${CDI_CHART:-$("${CHARTGEN[@]}" latest --project cdi)}
KV_TAG=$(appversion "$KUBEVIRT_CHART")
CDI_TAG=$(appversion "$CDI_CHART")
KV_PREV=$("${CHARTGEN[@]}" previous --project kubevirt --tag "$KV_TAG")
CDI_PREV=$("${CHARTGEN[@]}" previous --project cdi --tag "$CDI_TAG")
echo "KubeVirt: $KV_PREV -> $KV_TAG ($KUBEVIRT_CHART)"
echo "CDI:      $CDI_PREV -> $CDI_TAG ($CDI_CHART)"
MULTUS_CRDS_CHART=${MULTUS_CRDS_CHART:-$("${CHARTGEN[@]}" latest --project multus-crds)}
MULTUS_TAG=$(appversion "$MULTUS_CRDS_CHART")
MULTUS_PREV=$("${CHARTGEN[@]}" previous --project multus-crds --tag "$MULTUS_TAG")
echo "Multus:   $MULTUS_PREV -> $MULTUS_TAG ($MULTUS_CRDS_CHART)"

# Use the same Helm flags as k3s helm-controller with takeOwnership: true. It passes
# --take-ownership to install and upgrade. With Helm v4, it also passes
# --server-side=false, because take ownership needs client-side apply.
HELM_FLAGS=(--take-ownership)
HELM_MAJOR=$($HELM version --template '{{.Version}}' | sed 's/^v\([0-9]*\).*/\1/')
if [[ "$HELM_MAJOR" -ge 4 ]]; then
  HELM_FLAGS+=(--server-side=false)
fi
echo "Helm $($HELM version --template '{{.Version}}'), flags: ${HELM_FLAGS[*]}"
ADOPT_VALUES=()

log "Generate the charts for N"
KV_PREV_CHART=$("${CHARTGEN[@]}" generate --project kubevirt --tag "$KV_PREV" --charts-dir "$WORK/charts")
CDI_PREV_CHART=$("${CHARTGEN[@]}" generate --project cdi --tag "$CDI_PREV" --charts-dir "$WORK/charts")
MULTUS_PREV_CHART=$("${CHARTGEN[@]}" generate --project multus-crds --tag "$MULTUS_PREV" --charts-dir "$WORK/charts")

if [[ "${E2E_REUSE_CLUSTER:-}" != 1 ]]; then
  log "Create kind cluster $CLUSTER"
  $KIND create cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG" --wait 3m
else
  $KIND get kubeconfig --name "$CLUSTER" >"$KUBECONFIG"
fi

download() { curl -fsSL -o "$WORK/$3" "https://github.com/$1/releases/download/$2/$3"; }

NAD_CRD=network-attachment-definitions.k8s.cni.cncf.io
log "Multus CRDs: apply the upstream CRD of $MULTUS_PREV with kubectl apply"
curl -fsSL -o "$WORK/multus-daemonset.yml" \
  "https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/$MULTUS_PREV/deployments/multus-daemonset.yml"
# Keep only the CRD documents, as text, exactly as upstream wrote them.
awk '
  function flush() { if (doc ~ /\nkind: CustomResourceDefinition\n/) printf "---%s\n", doc; doc = "" }
  /^---/ { flush(); next }
  { doc = doc "\n" $0 }
  END { flush() }
' "$WORK/multus-daemonset.yml" >"$WORK/multus-crds.yaml"
[[ $(grep -c '^kind: CustomResourceDefinition' "$WORK/multus-crds.yaml") == 1 ]] \
  || fail "expected one CRD in the upstream multus-daemonset.yml"
$KUBECTL apply -f "$WORK/multus-crds.yaml"
$KUBECTL wait --for=condition=Established "crd/$NAD_CRD" --timeout=120s
cat <<'NAD' | $KUBECTL apply -f -
apiVersion: k8s.cni.cncf.io/v1
kind: NetworkAttachmentDefinition
metadata:
  name: egress-external
  namespace: default
spec:
  config: '{"cniVersion":"0.3.1","type":"bridge","bridge":"br-e2e"}'
NAD

# crd_state prints the identity, generation, and spec of the CRD, and the identity of
# the NetworkAttachmentDefinition. A CRD spec change increments the generation.
crd_state() {
  $KUBECTL get crd "$NAD_CRD" -o jsonpath='crd uid={.metadata.uid} generation={.metadata.generation}{"\n"}'
  $KUBECTL get crd "$NAD_CRD" -o jsonpath='{.spec}' | sha256sum | sed 's/^/crd spec sha256=/; s/ *-$//'
  $KUBECTL -n default get network-attachment-definitions egress-external \
    -o jsonpath='nad uid={.metadata.uid} config={.spec.config}{"\n"}'
}
crd_state >"$WORK/multus-before"
cat "$WORK/multus-before"

log "Multus CRDs: adopt with helm install --take-ownership at $MULTUS_PREV"
$HELM install multus-crds "$MULTUS_PREV_CHART" -n kube-system "${HELM_FLAGS[@]}" --wait --timeout 5m
crd_state >"$WORK/multus-after-adopt"
diff -u "$WORK/multus-before" "$WORK/multus-after-adopt" || fail "adoption changed the CRD or the NetworkAttachmentDefinition"
[[ $($KUBECTL get crd "$NAD_CRD" -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}') == Helm ]] \
  || fail "CRD $NAD_CRD is not managed by Helm"
[[ $($KUBECTL get crd "$NAD_CRD" -o jsonpath='{.metadata.annotations.helm\.sh/resource-policy}') == keep ]] \
  || fail "CRD $NAD_CRD has no keep annotation"
echo "adoption changed neither the CRD spec nor the NetworkAttachmentDefinition"

log "Multus CRDs: upgrade with Helm to $MULTUS_TAG"
$HELM upgrade multus-crds "$MULTUS_CRDS_CHART" -n kube-system "${HELM_FLAGS[@]}" --wait --timeout 5m
crd_state >"$WORK/multus-after-upgrade"
# The CRD of N+1 can differ from N, so compare only the identities.
diff -u <(grep -v '^crd spec' "$WORK/multus-before" | sed 's/ generation=.*//') \
  <(grep -v '^crd spec' "$WORK/multus-after-upgrade" | sed 's/ generation=.*//') \
  || fail "the upgrade replaced the CRD or the NetworkAttachmentDefinition"
[[ $($HELM get manifest multus-crds -n kube-system | grep -c '^kind: CustomResourceDefinition') == 1 ]] \
  || fail "release multus-crds does not contain the CRD"

log "Multus CRDs: helm uninstall"
$HELM uninstall multus-crds -n kube-system --wait
sleep 5
crd_state >"$WORK/multus-after-uninstall"
diff -u "$WORK/multus-after-upgrade" "$WORK/multus-after-uninstall" \
  || fail "helm uninstall changed the CRD or the NetworkAttachmentDefinition"
echo "helm uninstall left the CRD and the NetworkAttachmentDefinition in place"

log "Install upstream KubeVirt $KV_PREV and CDI $CDI_PREV with kubectl apply"
download kubevirt/kubevirt "$KV_PREV" kubevirt-operator.yaml
download kubevirt/kubevirt "$KV_PREV" kubevirt-cr.yaml
download kubevirt/containerized-data-importer "$CDI_PREV" cdi-operator.yaml
download kubevirt/containerized-data-importer "$CDI_PREV" cdi-cr.yaml
# Client-side apply, the same command that ruddervirt-setup runs.
$KUBECTL apply -f "$WORK/kubevirt-operator.yaml"
$KUBECTL wait --for=condition=Established crd/kubevirts.kubevirt.io --timeout=300s
$KUBECTL apply -f "$WORK/kubevirt-cr.yaml"
$KUBECTL apply -f "$WORK/cdi-operator.yaml"
$KUBECTL wait --for=condition=Established crd/cdis.cdi.kubevirt.io --timeout=300s
$KUBECTL apply -f "$WORK/cdi-cr.yaml"

log "Patch feature gates into the KubeVirt CR (like Aileron)"
GATES='["HostDevices","Sidecar"]'
$KUBECTL -n $KV_NS patch kubevirt kubevirt --type merge -p \
  '{"spec":{"configuration":{"developerConfiguration":{"featureGates":'"$GATES"',"useEmulation":true}}}}'

wait_kv() {
  local tag=$1
  for _ in $(seq 1 120); do
    local observed avail
    observed=$($KUBECTL -n $KV_NS get kubevirt kubevirt -o jsonpath='{.status.observedKubeVirtVersion}' 2>/dev/null || true)
    avail=$($KUBECTL -n $KV_NS get kubevirt kubevirt -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || true)
    if [[ "$observed" == "$tag" && "$avail" == True ]]; then
      echo "KubeVirt observedKubeVirtVersion=$observed Available=$avail"
      return 0
    fi
    sleep 10
  done
  fail "KubeVirt did not report $tag and Available (observed=$observed)"
}

wait_cdi() {
  local tag=$1
  for _ in $(seq 1 120); do
    local observed avail
    observed=$($KUBECTL get cdi cdi -o jsonpath='{.status.observedVersion}' 2>/dev/null || true)
    avail=$($KUBECTL get cdi cdi -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || true)
    if [[ "$observed" == "$tag" && "$avail" == True ]]; then
      echo "CDI observedVersion=$observed Available=$avail"
      return 0
    fi
    sleep 10
  done
  fail "CDI did not report $tag and Available (observed=$observed)"
}

# snapshot prints the identity and restart count of every long-running pod, every
# ReplicaSet, and every ControllerRevision (DaemonSet template) in both namespaces.
# A new pod template makes a new ReplicaSet or ControllerRevision.
# It does not print Deployment generations: Kubernetes increments the generation of
# a Deployment when its annotations change, and Helm adds annotations on adoption.
snapshot() {
  for ns in $KV_NS $CDI_NS; do
    $KUBECTL -n "$ns" get pods -l '!batch.kubernetes.io/job-name' \
      -o jsonpath='{range .items[*]}pod {.metadata.namespace}/{.metadata.name} uid={.metadata.uid} restarts={.status.containerStatuses[*].restartCount}{"\n"}{end}'
    $KUBECTL -n "$ns" get replicasets,controllerrevisions \
      -o jsonpath='{range .items[*]}{.kind} {.metadata.namespace}/{.metadata.name} uid={.metadata.uid}{"\n"}{end}'
  done | sort
}

check_gates() {
  local got
  got=$($KUBECTL -n $KV_NS get kubevirt kubevirt -o jsonpath='{.spec.configuration.developerConfiguration.featureGates}')
  [[ "$got" == "$GATES" ]] || fail "feature gates after $1: got $got, want $GATES"
  echo "feature gates after $1: $got"
}

wait_kv "$KV_PREV"
wait_cdi "$CDI_PREV"
sleep 30 # let the operators settle
snapshot >"$WORK/before-adopt"
cat "$WORK/before-adopt"

log "Adopt with helm install --take-ownership at N"
$HELM install kubevirt "$KV_PREV_CHART" -n $KV_NS "${HELM_FLAGS[@]}" "${ADOPT_VALUES[@]}" --wait --timeout 10m
$HELM install cdi "$CDI_PREV_CHART" -n $CDI_NS "${HELM_FLAGS[@]}" --wait --timeout 10m
sleep 60 # give the operators time to react to any change
wait_kv "$KV_PREV"
wait_cdi "$CDI_PREV"
snapshot >"$WORK/after-adopt"
if ! diff -u "$WORK/before-adopt" "$WORK/after-adopt"; then
  fail "adoption changed pods or pod templates"
fi
echo "no pod restarted, no pod template changed"
check_gates adoption
# KubeVirt rolls all components when uninstallStrategy changes, so the chart leaves
# it out on adoption at the running version.
[[ -z $($KUBECTL -n $KV_NS get kubevirt kubevirt -o jsonpath='{.spec.uninstallStrategy}') ]] \
  || fail "adoption set uninstallStrategy on KubeVirt"
[[ $($KUBECTL get cdi cdi -o jsonpath='{.spec.uninstallStrategy}') == BlockUninstallIfWorkloadsExist ]] \
  || fail "adoption did not set uninstallStrategy on CDI"
[[ $($KUBECTL -n $KV_NS get deploy virt-operator -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}') == Helm ]] \
  || fail "virt-operator is not managed by Helm"
[[ $($KUBECTL get ns $KV_NS -o jsonpath='{.metadata.labels.pod-security\.kubernetes\.io/enforce}') == privileged ]] \
  || fail "namespace $KV_NS has no privileged label"

log "Upgrade with Helm to N+1"
$HELM upgrade kubevirt "$KUBEVIRT_CHART" -n $KV_NS "${HELM_FLAGS[@]}" --wait --timeout 15m
$HELM upgrade cdi "$CDI_CHART" -n $CDI_NS "${HELM_FLAGS[@]}" --wait --timeout 15m
wait_kv "$KV_TAG"
wait_cdi "$CDI_TAG"
check_gates upgrade
[[ $($KUBECTL -n $KV_NS get kubevirt kubevirt -o jsonpath='{.spec.uninstallStrategy}') == BlockUninstallIfWorkloadsExist ]] \
  || fail "upgrade did not set uninstallStrategy on KubeVirt"
for crd in kubevirts.kubevirt.io cdis.cdi.kubevirt.io; do
  $KUBECTL get crd "$crd" -o jsonpath='{.metadata.managedFields[*].manager}' | grep -q chart-crd-hook \
    || fail "the CRD hook did not apply $crd"
done
echo "the CRD hooks applied both static CRDs"

log "Uninstall test"
cat <<'EOF' | $KUBECTL apply -f -
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: e2e-vm
  namespace: default
spec:
  runStrategy: Halted
  template:
    spec:
      domain:
        devices: {}
        resources:
          requests:
            memory: 64Mi
EOF
sleep 30
snapshot >"$WORK/before-uninstall"
$HELM uninstall kubevirt -n $KV_NS --wait
$HELM uninstall cdi -n $CDI_NS --wait
sleep 60
snapshot >"$WORK/after-uninstall"
if ! diff -u "$WORK/before-uninstall" "$WORK/after-uninstall"; then
  fail "helm uninstall changed pods or pod templates"
fi
$KUBECTL -n $KV_NS get kubevirt kubevirt >/dev/null || fail "KubeVirt CR deleted"
$KUBECTL get cdi cdi >/dev/null || fail "CDI CR deleted"
for crd in kubevirts.kubevirt.io virtualmachines.kubevirt.io cdis.cdi.kubevirt.io datavolumes.cdi.kubevirt.io; do
  $KUBECTL get crd "$crd" >/dev/null || fail "CRD $crd deleted"
done
$KUBECTL -n $KV_NS get deploy virt-operator virt-api virt-controller >/dev/null || fail "KubeVirt Deployments deleted"
$KUBECTL -n $KV_NS get ds virt-handler >/dev/null || fail "virt-handler deleted"
$KUBECTL -n $CDI_NS get deploy cdi-operator cdi-deployment >/dev/null || fail "CDI Deployments deleted"
$KUBECTL -n default get vm e2e-vm >/dev/null || fail "VirtualMachine deleted"
$KUBECTL get clusterrole kubevirt.io:operator cdi-operator-cluster >/dev/null || fail "operator RBAC deleted"
[[ -z $($HELM list -A -q) ]] || fail "Helm releases left"
wait_kv "$KV_TAG"
wait_cdi "$CDI_TAG"
echo "helm uninstall left the CRs, CRDs, operators, components, and the VM in place"

log "PASS"
