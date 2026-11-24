# Multus CRDs chart

The `multus-crds` chart installs the NetworkAttachmentDefinition CRD (`network-attachment-definitions.k8s.cni.cncf.io`) of Multus CNI. It is published at `oci://ghcr.io/ruddervirt/charts/multus-crds`.

ruddervirt-setup installs Multus with the `rke2-multus` chart from https://rke2-charts.rancher.io. That chart has no CRD: it has no `crds/` directory and no CRD template. On RKE2, a separate static manifest installs the CRD, but plain k3s has no such manifest. This chart fills that gap, so that a k3s HelmChart installs the CRD, as it does for the `kubevirt` and `cdi` charts.

For the generator, the version scheme, the tests, and the workflows, see the [README](../README.md).

## Contents

- [Chart content](#chart-content)
- [Why the CRD is in templates/](#why-the-crd-is-in-templates)
- [Which version to use](#which-version-to-use)
- [Install with k3s](#install-with-k3s)
- [Adopt a CRD that kubectl apply made](#adopt-a-crd-that-kubectl-apply-made)
- [Upgrade](#upgrade)
- [Uninstall behavior](#uninstall-behavior)
- [End-to-end test](#end-to-end-test)

## Chart content

The generator downloads `deployments/multus-daemonset.yml` from the `k8snetworkplumbingwg/multus-cni` repository at the release tag. This file is a repository file, not a release asset. The generator keeps only the CustomResourceDefinition `network-attachment-definitions.k8s.cni.cncf.io` and drops all other documents: the RBAC, the ServiceAccount, the ConfigMap, and the DaemonSet.

| File | Content |
| --- | --- |
| `templates/network-attachment-definitions.k8s.cni.cncf.io.yaml` | The upstream CRD with one added annotation, `helm.sh/resource-policy: keep`. |
| `templates/NOTES.txt` | A short note after install. |
| `values.yaml` | No values. |

The chart has no Namespace, no Deployment, no custom resource, no image values, and no hooks.

The rendered CRD has the same content as the upstream document. Only the `keep` annotation is added. Thus an adoption of a CRD that `kubectl apply` made does not change the CRD spec. `test/diff` compares the rendered CRD with the upstream document field by field.

If upstream adds a CustomResourceDefinition to `multus-daemonset.yml`, the generator stops with an error. A person must then review it and add its name to `CRDNames` in `internal/upstream/upstream.go`. If the listed CRD is missing or renamed, the generator also stops.

## Why the CRD is in templates/

The CRD is a normal template in `templates/`. It is not in `crds/`, and the chart does not use the CRD hook of the `kubevirt` and `cdi` charts. The reasons are:

- Helm installs files in `crds/` only on the first install. It never upgrades them. A CRD in `templates/` is part of the release manifest, so `helm upgrade` applies a new upstream CRD version like any other resource.
- The CRD is the only resource of the chart. A hook needs a ConfigMap, a ServiceAccount, a ClusterRole, a ClusterRoleBinding, a Job, and a kubectl image in `kube-system`. A template needs none of these.
- Helm adopts a template resource with `--take-ownership`, like the other resources of the `kubevirt` and `cdi` charts. Helm never adopts a file in `crds/`. It skips a CRD in `crds/` that already exists.
- The risk of a CRD in `templates/` is that `helm uninstall` deletes it. The `helm.sh/resource-policy: keep` annotation stops that. `test/diff` and the end-to-end test make sure that the annotation is present and that the CRD stays after uninstall.

The `kubevirt` and `cdi` charts keep the CRD hook for a different reason. Their custom resource is in the same release, so the static CRD must exist before Helm applies the templates.

## Which version to use

The chart version is the multus-cni tag without "v". Use the version that matches the `appVersion` of the `rke2-multus` chart:

| rke2-multus | appVersion | multus-crds chart |
| --- | --- | --- |
| `v4.3.017` | `4.3.0` | `4.3.0` |
| `v4.3.118` | `4.3.1` | `4.3.1` |

The NetworkAttachmentDefinition CRD is the same in multus-cni v4.3.0, v4.3.1, and v4.3.2. Thus any of these chart versions works with both rke2-multus versions. The charts in this repository start at `4.3.0`, the `MinTag` of the project.

## Install with k3s

`examples/helmchart-multus-crds.yaml` is the HelmChart resource:

```yaml
apiVersion: helm.cattle.io/v1
kind: HelmChart
metadata:
  name: multus-crds
  namespace: kube-system
spec:
  chart: oci://ghcr.io/ruddervirt/charts/multus-crds
  version: 4.3.1
  targetNamespace: kube-system
  takeOwnership: true
  failurePolicy: abort
```

The CRD is cluster scoped, so the release namespace has no effect on it. Install this chart before anything creates a NetworkAttachmentDefinition, for example before Aileron creates `egress-external`.

Do not install this chart on RKE2. On RKE2, the static manifest of RKE2 also manages the CRD, and two managers of one CRD conflict.

## Adopt a CRD that kubectl apply made

Use this procedure on a k3s cluster where ruddervirt-setup applied the CRD with `kubectl`.

1. Stop the `kubectl apply` of the CRD in ruddervirt-setup. After the adoption, Helm owns the CRD.
2. Apply the HelmChart resource. Keep `takeOwnership: true`.

   ```sh
   kubectl apply -f examples/helmchart-multus-crds.yaml
   ```

3. Wait for the helm-controller Job to complete:

   ```sh
   kubectl -n kube-system get job helm-install-multus-crds
   ```

4. Make sure that Helm owns the CRD and that the NetworkAttachmentDefinitions still exist:

   ```sh
   kubectl get crd network-attachment-definitions.k8s.cni.cncf.io \
     -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by} {.metadata.annotations.helm\.sh/resource-policy}{"\n"}'
   kubectl get network-attachment-definitions -A
   ```

   The first command prints `Helm keep`.

On adoption, Helm builds a three-way merge patch. The "original" object in that patch is the rendered manifest. Thus the patch can only add or change fields that the chart renders. The rendered CRD equals the live CRD, so Helm adds only its labels, its annotations, and the `keep` annotation. The CRD spec and its `metadata.generation` do not change.

## Upgrade

Change `spec.version` in the HelmChart. helm-controller runs `helm upgrade`, and Helm applies the CRD of the new version. If a new upstream version removes a field from the CRD, Helm removes that field from the live CRD. NetworkAttachmentDefinitions are not deleted on upgrade.

## Uninstall behavior

`helm uninstall` never deletes the CRD. When someone deletes the HelmChart resource, k3s runs `helm uninstall`.

- The CRD has the annotation `helm.sh/resource-policy: keep`. Helm does not delete it on uninstall or on upgrade.
- The chart has no pre-delete or post-delete hooks.

This rule protects data. Deleting the CRD deletes every NetworkAttachmentDefinition in the cluster, for example `egress-external` that Aileron creates.

After `helm uninstall`, the CRD stays, but no Helm release owns it. To manage it with Helm again, install the chart again with `takeOwnership: true`.

To remove the CRD completely, delete every pod that uses a secondary network first. Then run this command:

```sh
kubectl delete crd network-attachment-definitions.k8s.cni.cncf.io
```

This command deletes all NetworkAttachmentDefinitions in the cluster.

## End-to-end test

`test/e2e/run.sh` tests this chart on a kind cluster before it tests KubeVirt and CDI. N is the stable multus-cni release before the chart under test.

1. Apply the CRD document of the upstream `multus-daemonset.yml` at N with `kubectl apply`. Create the NetworkAttachmentDefinition `default/egress-external`.
2. Adopt the CRD with `helm install --take-ownership` at N. With Helm v4, the test also uses `--server-side=false`, as helm-controller does. Make sure that the CRD has the same UID, generation, and spec, and that the NetworkAttachmentDefinition has the same UID. Make sure that the CRD has the Helm label and the `keep` annotation.
3. Upgrade to N+1 with `helm upgrade --take-ownership`. Make sure that the CRD and the NetworkAttachmentDefinition are the same objects.
4. Run `helm uninstall`. Make sure that the CRD and the NetworkAttachmentDefinition did not change.
