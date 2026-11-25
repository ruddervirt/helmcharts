# Kube-OVN CRDs chart

The `kube-ovn-crds` chart installs the 21 CustomResourceDefinitions (CRDs) of Kube-OVN. It is published at `oci://ghcr.io/ruddervirt/charts/kube-ovn-crds`.

The upstream `kube-ovn-v2` chart has its CRDs in `crds/`. Helm installs files in `crds/` only on the first install and never upgrades them. Thus a `kube-ovn-v2` upgrade leaves the CRDs at the version of the first install. Stabilizer runs this chart as a separate k3s HelmChart, so that the CRDs upgrade before Kube-OVN does.

For the generator, the version scheme, the tests, and the workflows, see the [README](../README.md).

## Contents

- [Chart content](#chart-content)
- [Which version to use](#which-version-to-use)
- [Install with k3s](#install-with-k3s)
- [Do not set forceConflicts](#do-not-set-forceconflicts)
- [Adopt the CRDs of kube-ovn-v2](#adopt-the-crds-of-kube-ovn-v2)
- [Upgrade](#upgrade)
- [Uninstall behavior](#uninstall-behavior)
- [End-to-end test](#end-to-end-test)

## Chart content

The generator downloads `charts/kube-ovn-v2/crds/kube-ovn-crd.yaml` from the `kubeovn/kube-ovn` repository at the release tag. The file has only CRDs. The chart has one template per CRD in `templates/`, with one added annotation, `helm.sh/resource-policy: keep`. It has no values and no hooks.

The chart contains these CRDs. Each name ends in `.kubeovn.io`.

| Area | CRDs |
| --- | --- |
| VPC | `vpcs`, `vpc-dnses`, `vpc-nat-gateways`, `vpc-egress-gateways` |
| iptables NAT | `iptables-eips`, `iptables-fip-rules`, `iptables-dnat-rules`, `iptables-snat-rules` |
| OVN NAT | `ovn-eips`, `ovn-fips`, `ovn-snat-rules`, `ovn-dnat-rules` |
| Addresses | `ips`, `vips`, `subnets`, `ippools` |
| Networks | `vlans`, `provider-networks`, `switch-lb-rules` |
| Policy | `security-groups`, `qos-policies` |

The rendered CRDs have the same content as upstream. `test/diff` compares each rendered CRD with the upstream document field by field.

If upstream adds a CRD to the file, the generator stops with an error. A person must then review it and add its name to `CRDNames` in `internal/upstream/upstream.go`. Kube-OVN v1.16 adds `bgp-confs`, `dnsnameresolvers`, and `evpn-confs`. v1.17 also adds `router-lb-rules`.

The CRDs are in `templates/`, not in `crds/`, so that `helm upgrade` applies them. The reasons are the same as for the `multus-crds` chart. See [Why the CRD is in templates/](multus-crds.md#why-the-crd-is-in-templates).

## Which version to use

The chart version is the Kube-OVN tag without "v". The `kube-ovn-v2` chart uses the same version with a "v". For example, `kube-ovn-v2` `v1.15.35` matches `kube-ovn-crds` `1.15.35`. Use the version of the `kube-ovn-v2` chart that you install or upgrade to.

| Chart | Change to the CRDs |
| --- | --- |
| `1.15.19` | The first chart. In `vpc-egress-gateways`, `spec.bandwidth.ingress` and `spec.bandwidth.egress` are integers in Mbps. |
| `1.15.24` | `bandwidth.ingress` and `bandwidth.egress` are int-or-string. They accept an integer in Mbps or a quantity such as `1Gi`. Upstream made this change in a release between v1.15.19 and v1.15.24. |
| `1.15.32` | The CRDs are the same as in `1.15.24`. |
| `1.15.35` | The CRDs are the same as in `1.15.24`. |

## Install with k3s

`examples/helmchart-kube-ovn-crds.yaml` is the HelmChart resource:

```yaml
apiVersion: helm.cattle.io/v1
kind: HelmChart
metadata:
  name: kube-ovn-crds
  namespace: kube-system
spec:
  chart: oci://ghcr.io/ruddervirt/charts/kube-ovn-crds
  version: 1.15.35
  targetNamespace: kube-system
  takeOwnership: true
  failurePolicy: abort
```

The CRDs are cluster scoped, so the release namespace has no effect on them.

Install or upgrade this chart before `kube-ovn-v2`. The `kube-ovn-v2` chart does not conflict with this chart. If the CRD already exists, Helm skips its file in `crds/` on install. On upgrade, Helm ignores `crds/`.

## Do not set forceConflicts

Do not set `forceConflicts: true` together with `takeOwnership: true`. The helm-controller Job fails with this error:

```
Error: INSTALLATION FAILED: invalid client update option(s): forceConflicts enabled when serverSideApply disabled
```

The reason is:

1. With `takeOwnership: true`, helm-controller sets `--server-side=false`. Helm then uses client-side apply.
2. klipper-helm is the image of the helm-controller Job. If server-side apply is off, klipper-helm means to drop `--force-conflicts`. Its test `[[ "${SERVER_SIDE}" == ^false$ ]]` compares with the literal text `^false$` and never matches. Thus it passes `--server-side=false --force-conflicts=true`. This is true in klipper-helm v0.14.0-build20261117, which has Helm v4.3.1.
3. Helm v4 refuses `--force-conflicts` without server-side apply.

`forceConflicts` is not necessary for the adoption. The live CRDs have the field managers `helm` (from `crds/` of `kube-ovn-v2`) and `kubectl-client-side-apply` (from `kubectl apply`). Field managers cause conflicts only in server-side apply. Client-side apply ignores them. If an example HelmChart sets both `takeOwnership` and `forceConflicts`, `test/diff` fails.

## Adopt the CRDs of kube-ovn-v2

Use this procedure on a k3s cluster where `kube-ovn-v2` or `kubectl apply` made the CRDs.

1. Stop any `kubectl apply` of the Kube-OVN CRDs. After the adoption, Helm owns the CRDs.
2. Apply the HelmChart resource. Set `spec.version` to the version of the running `kube-ovn-v2` chart. Keep `takeOwnership: true`.

   ```sh
   kubectl apply -f examples/helmchart-kube-ovn-crds.yaml
   ```

3. Wait for the helm-controller Job to complete:

   ```sh
   kubectl -n kube-system get job helm-install-kube-ovn-crds
   ```

4. Make sure that Helm owns every CRD:

   ```sh
   kubectl get crd -o custom-columns='NAME:.metadata.name,MANAGED-BY:.metadata.labels.app\.kubernetes\.io/managed-by,POLICY:.metadata.annotations.helm\.sh/resource-policy' | grep kubeovn.io
   ```

   Each of the 21 lines shows `Helm` and `keep`.

The live CRDs can be older than the running `kube-ovn-v2`, because `crds/` never upgrades. The adoption then also updates them. A test on kind adopted CRDs of `v1.15.19` directly with the chart `1.15.35`, with Helm v3.19.2 and with Helm v4.3.1. In both cases, `bandwidth` became int-or-string, without `type: integer` or `format: int64`.

## Upgrade

Change `spec.version` in the HelmChart before you upgrade `kube-ovn-v2`. helm-controller runs `helm upgrade`, and Helm applies the CRDs of the new version. If the new version removes a field from a CRD, Helm removes that field from the live CRD. Custom resources are not deleted on upgrade.

An upgrade from `1.15.19` to a later chart changes `bandwidth` in `vpc-egress-gateways` to int-or-string. Existing VpcEgressGateways with an integer bandwidth stay valid.

## Uninstall behavior

`helm uninstall` never deletes the CRDs. When someone deletes the HelmChart resource, k3s runs `helm uninstall`.

- Each CRD has the annotation `helm.sh/resource-policy: keep`. Helm does not delete it on uninstall or on upgrade.
- The chart has no pre-delete or post-delete hooks.

This rule protects data. Deleting a CRD deletes every custom resource of that kind in the cluster, for example every Subnet, Vpc, or IP.

After `helm uninstall`, the CRDs stay, but no Helm release owns them. To manage them with Helm again, install the chart again with `takeOwnership: true`.

To remove the CRDs completely, uninstall Kube-OVN first. Then run this command:

```sh
kubectl get crd -o name | grep '\.kubeovn\.io$' | xargs kubectl delete
```

This command deletes all Kube-OVN custom resources in the cluster.

## End-to-end test

`test/e2e/run.sh` tests this chart on a kind cluster. `KUBEOVN_FROM` is the Kube-OVN tag of the CRDs before adoption. The default is `v1.15.19`.

1. Create the upstream CRDs of `KUBEOVN_FROM` with the field manager `helm`, as Helm does for `crds/`. Then run `kubectl apply` on the same file, which adds the manager `kubectl-client-side-apply`. Create a Vpc and a VpcEgressGateway with an integer bandwidth. Make sure that the CRD rejects a bandwidth of `1Gi`.
2. Adopt the CRDs with `helm install --take-ownership` at `KUBEOVN_FROM`. With Helm v4, the test also uses `--server-side=false`, as helm-controller does. Make sure that no CRD changed its UID, generation, or spec, and that the custom resources have the same UIDs. Make sure that each CRD has the Helm label and the `keep` annotation.
3. Upgrade to the newest chart with `helm upgrade --take-ownership`. Make sure that the CRDs and the custom resources are the same objects. Make sure that `bandwidth.ingress` of `vpc-egress-gateways` is int-or-string, and that the CRD accepts a bandwidth of `1Gi`.
4. Run `helm uninstall`. Make sure that all 21 CRDs and the custom resources did not change.
