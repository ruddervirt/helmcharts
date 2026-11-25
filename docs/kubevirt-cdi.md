# KubeVirt and CDI charts

The `kubevirt` and `cdi` charts install the KubeVirt operator and the CDI (Containerized Data Importer) operator, each with its custom resource. They are published at:

- `oci://ghcr.io/ruddervirt/charts/kubevirt`
- `oci://ghcr.io/ruddervirt/charts/cdi`

ruddervirt-setup installs both charts through the k3s HelmChart resource (`helm.cattle.io/v1`). The charts can adopt an existing install that `kubectl apply` made, with no restart of any component.

For the generator, the version scheme, the tests, and the workflows, see the [README](../README.md).

## Contents

- [Chart layout](#chart-layout)
- [Values](#values)
  - [spec.imageTag for KubeVirt](#specimagetag-for-kubevirt)
- [Custom resource fields and Helm merge behavior](#custom-resource-fields-and-helm-merge-behavior)
- [Install with k3s](#install-with-k3s)
- [Adopt an install that kubectl apply made](#adopt-an-install-that-kubectl-apply-made)
- [Upgrade](#upgrade)
- [Uninstall behavior](#uninstall-behavior)
- [End-to-end test](#end-to-end-test)

## Chart layout

Each chart version is in `charts/<name>/<version>/`. The generator downloads `<x>-operator.yaml` and `<x>-cr.yaml` from the upstream release and splits them:

| File | Content |
| --- | --- |
| `crds/<crd>.yaml` | The one static CRD (`kubevirts.kubevirt.io` or `cdis.cdi.kubevirt.io`). The operator creates all other CRDs at runtime. |
| `templates/<x>-operator.yaml` | The upstream operator manifest without the Namespace. Namespaces are `.Release.Namespace`. Images and the version are values. |
| `templates/<x>.yaml` | The KubeVirt or CDI custom resource. Only the fields in `.Values.<x>.spec` render. |
| `templates/hook-namespace.yaml` | A pre-install and pre-upgrade Job that labels the release namespace with the labels of the upstream Namespace. |
| `templates/hook-crd.yaml` | A pre-install and pre-upgrade Job that applies the static CRD from a ConfigMap, because Helm never upgrades files in `crds/`. |

Resource names and kinds are the upstream names, for example `virt-operator`, `kubevirt-operator`, `kubevirt.io:operator`, `kubevirt-cluster-critical`, `cdi-operator`, and `cdi-operator-cluster`. Helm adopts existing resources by name. If an upstream name changes, the generator stops.

The images are the upstream images (`quay.io/kubevirt/*`). The version strings keep the "v" prefix. For example, `KUBEVIRT_VERSION` is `v1.9.1`, because ruddervirt-setup parses `status.observedKubeVirtVersion` as `vMAJOR.MINOR.PATCH`.

The charts work in any release namespace. ruddervirt-setup uses `kubevirt` and `cdi`. The upstream manifests use the same namespaces.

## Values

### KubeVirt chart

| Value | Default | Description |
| --- | --- | --- |
| `image.registry` | `quay.io/kubevirt` | Registry and repository prefix of every image. |
| `image.tag` | upstream tag, for example `v1.9.1` | Tag of every image. The chart also writes it to `KUBEVIRT_VERSION`. Keep the "v". |
| `image.overrides` | `{}` | Full image reference by image name, for example `virt-operator: registry.example.com/virt-operator@sha256:...`. |
| `kubevirt.annotations` | `{}` | Extra annotations on the KubeVirt CR. |
| `kubevirt.labels` | `{}` | Extra labels on the KubeVirt CR. |
| `kubevirt.spec` | see below | The spec of the KubeVirt CR. It renders as is. |
| `hooks.image` | `registry.k8s.io/kubectl:v1.34.2` | Image of the hook Jobs. Its entrypoint must be `kubectl`. |
| `hooks.namespaceLabels.enabled` | `true` | Run the namespace label hook. |
| `hooks.crd.enabled` | `true` | Run the CRD apply hook. |
| `hooks.nodeSelector`, `hooks.tolerations`, `hooks.resources` | empty | Scheduling and resources of the hook Jobs. |

The default `kubevirt.spec` for v1.9.1:

```yaml
kubevirt:
  spec:
    configuration:
      imagePullPolicy: IfNotPresent
    imagePullPolicy: IfNotPresent
    uninstallStrategy: BlockUninstallIfWorkloadsExist
```

### CDI chart

The CDI chart has the same values. The custom resource key is `cdi`. The image names are `cdi-operator`, `cdi-controller`, `cdi-importer`, `cdi-cloner`, `cdi-apiserver`, `cdi-uploadserver`, and `cdi-uploadproxy`. `image.tag` also sets `OPERATOR_VERSION`.

The default `cdi.spec` for v1.65.0:

```yaml
cdi:
  spec:
    config:
      featureGates:
        - HonorWaitForFirstConsumer
        - WebhookPvcRendering
    imagePullPolicy: IfNotPresent
    infra:
      nodeSelector:
        kubernetes.io/os: linux
    uninstallStrategy: BlockUninstallIfWorkloadsExist
    workload:
      nodeSelector:
        kubernetes.io/os: linux
```

### How the defaults are made

The default spec is the spec of the upstream `kubevirt-cr.yaml` or `cdi-cr.yaml` for that tag, with two changes:

1. The generator removes empty maps and empty lists, for example `featureGates: []` and `workloadUpdateStrategy: {}`. These fields have no effect. A rendered field is a field that Helm owns. Helm can then overwrite values that other controllers set in it.
2. The generator adds `uninstallStrategy: BlockUninstallIfWorkloadsExist`. The upstream files leave this field empty, and KubeVirt and CDI treat an empty value as `RemoveWorkloads`.

### Deferred uninstallStrategy for KubeVirt

KubeVirt hashes the top-level string fields of the KubeVirt spec into its install strategy. `uninstallStrategy` is one of these fields. A change of the field makes virt-operator roll out virt-api, virt-controller, virt-handler, and the other components again. Running VMs continue to run, but the components restart.

To adopt with no restart, the KubeVirt chart uses the Helm `lookup` function. If all of these conditions are true, the chart leaves out `uninstallStrategy`:

- the live KubeVirt CR exists,
- the live CR has no `uninstallStrategy`,
- the live CR runs the chart version (`status.observedKubeVirtVersion` equals `image.tag`).

At the next upgrade to a new version, the chart renders the field. That upgrade rolls out the components anyway. A new install gets the field immediately. `helm template` has no cluster access, so it always renders the field.

CDI does not hash the field. The CDI chart always renders it.

### spec.imageTag for KubeVirt

virt-operator uses `spec.imageTag` of the KubeVirt CR for every component except itself. Older ruddervirt-os releases set this field with `kubectl patch`. The charts `1.8.2`, `1.9.1`, and `1.10.0` do not render the field. On those clusters, a chart upgrade moves virt-operator, but virt-api, virt-controller, and virt-handler stay at the old version.

The charts `1.9.1+fix.1`, `1.10.0+fix.1`, and all later KubeVirt charts use the Helm `lookup` function on the live KubeVirt CR. Two conditions must be true: the live CR has `spec.imageTag`, and `kubevirt.spec` in the values does not set `imageTag`. Then the chart renders `spec.imageTag` with the value of `image.tag`. Then a chart upgrade moves all components.

If the live CR has no `spec.imageTag`, the chart does not render the field. `helm template` has no cluster access, so it never renders the field. To pin another tag, set `kubevirt.spec.imageTag`. The chart never renders `spec.imageRegistry`.

Use a chart with this behavior to fix a cluster with a stale `spec.imageTag`. For example, a cluster on chart `1.10.0` with `imageTag: v1.8.2` needs an upgrade to `1.10.0+fix.1`.

An upgrade from `1.9.1+fix.1` to the published `1.10.0` removes `spec.imageTag`. The old release manifest has the field and the new one does not, so the Helm three-way merge deletes it. Then KubeVirt runs every component at the operator version.

`ChartsBeforeFollowImageTag` in `internal/upstream/upstream.go` lists the published charts `1.8.2`, `1.9.1`, and `1.10.0`, so that `chartgen regenerate` renders them exactly as published.

The operator Deployment, its RBAC, and the PriorityClass have no values except the images and the version. They render exactly as upstream, so an adoption does not change the pod template.

To remove a default field from the spec, set it to `null`:

```yaml
kubevirt:
  spec:
    imagePullPolicy: null
```

## Custom resource fields and Helm merge behavior

The chart renders only the custom resource fields that values set. Aileron patches feature gates into the KubeVirt CR at runtime. The chart does not render `spec.configuration.developerConfiguration.featureGates` by default, so Helm does not own that field.

When `takeOwnership: true` is set, k3s helm-controller runs Helm with `--take-ownership` and `--server-side=false`. Helm then uses client-side JSON merge patches for custom resources. Helm v3.17 and later, and Helm v4, behave the same way here.

### On adoption

Adoption is the first `helm install --take-ownership` over resources that already exist.

- Helm uses the rendered manifest as the "original" object. It builds a three-way merge patch from the original, the rendered manifest, and the live object.
- Because the original and the rendered manifest are the same, the patch can only add or change fields that the chart renders.
- Helm never deletes a live field on adoption. Feature gates, `useEmulation`, and other fields that the chart does not render stay as they are.
- Helm adds its labels (`app.kubernetes.io/managed-by: Helm`) and annotations (`meta.helm.sh/release-name`, `meta.helm.sh/release-namespace`) to each resource.

### On upgrade

- Helm builds a two-way merge patch from the manifest of the previous release and the new manifest. It applies that patch to the live object.
- A field that is in neither manifest is not touched. Feature gates that Aileron set stay in place.
- If a field is in the previous manifest and not in the new one, Helm deletes it. For example, you set `featureGates` in values and later remove that value. Helm then deletes the field.
- Lists are replaced as a whole. If a value sets `featureGates`, the chart owns the whole list and replaces the gates that Aileron set.

For CDI, the upstream default `config.featureGates` is not empty, so the chart owns `spec.config.featureGates` by default.

`test/merge` tests these rules with the same patch functions that Helm uses. The end-to-end test makes sure that the same rules apply on a real cluster.

## Install with k3s

`examples/helmchart-kubevirt.yaml` and `examples/helmchart-cdi.yaml` are the HelmChart resources that ruddervirt-setup uses:

```yaml
apiVersion: helm.cattle.io/v1
kind: HelmChart
metadata:
  name: kubevirt
  namespace: kube-system
spec:
  chart: oci://ghcr.io/ruddervirt/charts/kubevirt
  version: 1.9.1
  targetNamespace: kubevirt
  createNamespace: true
  takeOwnership: true
  failurePolicy: abort
```

The HelmChart name is the Helm release name. With `failurePolicy: abort`, helm-controller does not uninstall a failed release. It stops and keeps the resources.

Install KubeVirt before CDI. Both charts are independent. The CDI chart does not need KubeVirt.

## Adopt an install that kubectl apply made

Use this procedure on a cluster where ruddervirt-setup installed KubeVirt and CDI with `kubectl apply` of the upstream release YAML.

1. Find the version that runs now:

   ```sh
   kubectl -n kubevirt get kubevirt kubevirt -o jsonpath='{.status.observedKubeVirtVersion}{"\n"}'
   kubectl get cdi cdi -o jsonpath='{.status.observedVersion}{"\n"}'
   ```

2. Stop all `kubectl apply` of the upstream KubeVirt and CDI YAML. After the adoption, Helm owns these resources. A later `kubectl apply` of the upstream YAML fights with Helm.
3. Apply the two HelmChart resources. Set `version` to the running version without "v", for example `1.9.1` and `1.65.0`. Keep `takeOwnership: true`.

   ```sh
   kubectl apply -f examples/helmchart-kubevirt.yaml -f examples/helmchart-cdi.yaml
   ```

4. Wait for the helm-controller Jobs to complete:

   ```sh
   kubectl -n kube-system get jobs helm-install-kubevirt helm-install-cdi
   ```

5. Make sure that Helm owns the resources and that no pod restarted:

   ```sh
   kubectl -n kubevirt get deploy virt-operator -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}{"\n"}'
   kubectl -n kubevirt get pods
   kubectl -n cdi get pods
   ```

The adoption changes no pod, because the rendered operator Deployment is the same as the upstream Deployment. The CDI CR gets `uninstallStrategy: BlockUninstallIfWorkloadsExist`. CDI does not restart components for that change. The KubeVirt CR gets the field at the next upgrade. See [Deferred uninstallStrategy for KubeVirt](#deferred-uninstallstrategy-for-kubevirt).

The generation of the operator Deployments increments by one. Helm adds annotations. If the annotations of a Deployment change, Kubernetes increments the generation. The pod template does not change.

If you adopt with a newer chart version than the running version, the adoption is also an upgrade. The operator then rolls out the new version.

## Upgrade

Change `spec.version` in the HelmChart. helm-controller runs `helm upgrade`. Before the upgrade, the CRD hook applies the new static CRD with server-side apply. Then Helm updates the operator Deployment, and the operator upgrades all components.

Upgrade one minor version at a time, as the KubeVirt and CDI upgrade documents tell you.

## Uninstall behavior

`helm uninstall` never removes KubeVirt, CDI, or VMs. When someone deletes the HelmChart resource, k3s runs `helm uninstall`.

- Every resource in the charts has the annotation `helm.sh/resource-policy: keep`. Helm does not delete these resources on uninstall. This includes the custom resource, the operator Deployment, the RBAC, and the PriorityClass.
- Helm never deletes CRDs that it installed from `crds/`.
- The charts have no pre-delete or post-delete hooks. The SUSE Edge charts have hooks that delete the custom resource and the CRDs. These charts do not.
- The custom resources have `uninstallStrategy: BlockUninstallIfWorkloadsExist`. If someone deletes the KubeVirt CR by hand while VMIs exist, KubeVirt refuses to uninstall. An adopted KubeVirt CR gets this field at its first upgrade with the chart.

After `helm uninstall`, the resources stay, but no Helm release owns them. To manage them with Helm again, install the chart again with `takeOwnership: true`.

The `keep` policy also applies on upgrade. If a new upstream release drops a resource, Helm does not delete the old resource. You must delete it by hand.

### Remove KubeVirt and CDI completely

These steps delete KubeVirt, CDI, and all VMs. Do them only for a full removal:

1. Delete all VMs and DataVolumes.
2. Delete the HelmChart resources: `kubectl -n kube-system delete helmchart kubevirt cdi`.
3. Delete the custom resources and wait until the operators remove the components:

   ```sh
   kubectl -n kubevirt delete kubevirt kubevirt --wait
   kubectl delete cdi cdi --wait
   ```

4. Delete the operators with the upstream YAML of the installed version:

   ```sh
   kubectl delete -f https://github.com/kubevirt/kubevirt/releases/download/v1.9.1/kubevirt-operator.yaml
   kubectl delete -f https://github.com/kubevirt/containerized-data-importer/releases/download/v1.65.0/cdi-operator.yaml
   ```

## End-to-end test

`test/e2e/run.sh` needs Docker, kind, kubectl, helm, and Go. It uses its own kubeconfig file and does not change your current kubectl context. It does these steps on a new kind cluster:

1. Install upstream KubeVirt and CDI at version N (the release before the chart under test) with `kubectl apply`.
2. Patch feature gates and `useEmulation` into the KubeVirt CR, as Aileron does.
3. Adopt both installs with `helm install --take-ownership` at N. With Helm v4, it also uses `--server-side=false`, as helm-controller does.
4. Make sure that no pod restarted or was replaced, that no new ReplicaSet or ControllerRevision appeared, and that the feature gates stayed. Make sure that the KubeVirt CR has no `uninstallStrategy` yet.
5. Upgrade to N+1 with `helm upgrade --take-ownership`, as helm-controller does. Make sure that the operators report N+1, the CRD hooks ran, the feature gates stayed, and the KubeVirt CR has `uninstallStrategy: BlockUninstallIfWorkloadsExist`.
6. Create a VirtualMachine and run `helm uninstall`. Make sure that the custom resources, the CRDs, the operators, the components, the RBAC, and the VM stay in place, and that no pod changed.

```sh
test/e2e/run.sh
# Keep the cluster after the test:
E2E_KEEP_CLUSTER=1 test/e2e/run.sh
```
