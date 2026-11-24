# ruddervirt Helm charts

This repository turns upstream releases into Helm charts and publishes them as OCI artifacts under `oci://ghcr.io/ruddervirt/charts/`. Each chart version matches one upstream release tag.

A generator (`cmd/chartgen`) writes every file under `charts/`. Do not edit those files by hand. If `charts/` is different from the generator output, CI fails.

## Charts

| Chart | Upstream | OCI reference | Documentation |
| --- | --- | --- | --- |
| `kubevirt` | [kubevirt/kubevirt](https://github.com/kubevirt/kubevirt) | `oci://ghcr.io/ruddervirt/charts/kubevirt` | [docs/kubevirt-cdi.md](docs/kubevirt-cdi.md) |
| `cdi` | [kubevirt/containerized-data-importer](https://github.com/kubevirt/containerized-data-importer) | `oci://ghcr.io/ruddervirt/charts/cdi` | [docs/kubevirt-cdi.md](docs/kubevirt-cdi.md) |
| `multus-crds` | [k8snetworkplumbingwg/multus-cni](https://github.com/k8snetworkplumbingwg/multus-cni) | `oci://ghcr.io/ruddervirt/charts/multus-crds` | [docs/multus-crds.md](docs/multus-crds.md) |

The documentation of each chart covers its values, install, adoption, upgrade, and uninstall behavior.

## Contents

- [Principles](#principles)
- [Repository layout](#repository-layout)
- [Version scheme](#version-scheme)
- [Generator](#generator)
- [Add a new chart](#add-a-new-chart)
- [Tests](#tests)
- [GitHub Actions](#github-actions)
- [Verify a chart signature](#verify-a-chart-signature)
- [License](#license)

## Principles

All charts in this repository follow these rules:

- The charts render the upstream release manifests. Resource names, kinds, and images are the upstream values. Helm adopts existing resources by name, so a chart can take over an install that `kubectl apply` made.
- The charts work in any release namespace.
- `helm uninstall` removes nothing. Every resource has the annotation `helm.sh/resource-policy: keep`, and no chart has a pre-delete or post-delete hook.
- A custom resource renders only the fields that values set. A field that the chart does not render is not owned by Helm, so other controllers can manage it.
- If the upstream layout changes, the generator stops with an error. A person must review the change before a new chart is made.

## Repository layout

| Path | Content |
| --- | --- |
| `cmd/chartgen/` | The generator command. |
| `internal/upstream/` | The upstream projects: release files, expected layout, and tag rules. |
| `internal/chartgen/` | The code and templates that build a chart from the upstream files. |
| `internal/manifest/` | YAML helpers that the generator and the tests share. |
| `charts/<name>/<version>/` | Generated charts. Do not edit. |
| `docs/` | Documentation for each chart. |
| `examples/` | Example k3s HelmChart resources. |
| `test/` | Diff, merge, and end-to-end tests. |
| `hack/publish.sh` | Pushes and signs the charts that the registry does not have. |

## Version scheme

| Field | Value | Example |
| --- | --- | --- |
| Chart `version` | The upstream tag without "v" | `1.9.1` |
| Chart `appVersion` | The upstream tag | `v1.9.1` |
| Chart fix for the same upstream tag | `<version>+fix.<N>` | `1.9.1+fix.1` |

Helm and OCI tags need plain semver, so the chart version has no "v".

If a chart needs a fix for an upstream tag that is already published, generate it with `--fix N`:

```sh
go run ./cmd/chartgen generate --project kubevirt --tag v1.9.1 --fix 1
```

The new chart goes to `charts/kubevirt/1.9.1+fix.1/`. OCI tags cannot contain "+", so Helm stores it as the tag `1.9.1_fix.1`. Semver ignores build metadata in the sort order. Thus a version range does not prefer the fix. Pin the exact version (`version: 1.9.1+fix.1`) in the HelmChart.

## Generator

The generator is `cmd/chartgen`. It needs Go and network access to github.com.

```sh
# Generate one chart into charts/<project>/<version>/
go run ./cmd/chartgen generate --project kubevirt --tag v1.9.1

# Generate all charts in charts/ again (CI does this and fails on a difference)
go run ./cmd/chartgen regenerate

# List the stable upstream releases without a chart (JSON)
go run ./cmd/chartgen missing

# Other helpers
go run ./cmd/chartgen tags --project kubevirt
go run ./cmd/chartgen previous --project kubevirt --tag v1.9.1
go run ./cmd/chartgen latest --project kubevirt
```

Set `CHARTGEN_CACHE` to a directory to cache the downloaded release files. Set `GITHUB_TOKEN` to avoid the GitHub API rate limit.

A stable tag is `vMAJOR.MINOR.PATCH` with no suffix. The generator ignores `-rc`, `-alpha`, and `-beta` tags. It also ignores tags older than the `MinTag` of the project.

If the upstream layout changes, the generator stops with an error. Examples:

- the static CRD is missing, or there is more than one CRD,
- a new kind appears,
- a required object is missing or has a new name,
- the operator Deployment is missing,
- an image is not in the upstream registry, or has a different tag,
- a version env var or an image env var is missing,
- the tag or the upstream namespace is still in a value after templating,
- the custom resource file has an unexpected kind, name, or metadata,
- a CRD that a CRD-only project lists is missing,
- the source file of a CRD-only project has a CRD that the project does not list.

## Add a new chart

The generator supports two project shapes. The `Shape` field of a project in `internal/upstream/upstream.go` selects one:

| Shape | Upstream source | Chart content | Examples |
| --- | --- | --- | --- |
| `ShapeOperator` | An operator manifest and a custom resource file, both release assets. The manifest has one Namespace, one static CRD, and one operator Deployment. | The operator resources, the custom resource, the static CRD in `crds/`, and the namespace and CRD hooks. | `kubevirt`, `cdi` |
| `ShapeCRDs` | One repository file at the release tag. | Only the CRDs that the project lists, in `templates/`. All other documents of the file are dropped. | `multus-crds` |

A project with a different shape needs a change to `internal/chartgen` first.

To add a project:

1. Add a `Project` entry to `Projects` in `internal/upstream/upstream.go`. Set `Name`, `Repo`, `Shape`, and `MinTag`.
2. For `ShapeOperator`, set the release file names, the namespace, the CRD, the custom resource, and the Deployment. Also set the image registry, the env vars, and the required objects.
3. For `ShapeCRDs`, set `SourceFile` (the repository path) and `CRDNames`.
4. Add the name to `Names()` in the same file. The tests, `regenerate`, and `missing` use this list.
5. Generate the first chart: `go run ./cmd/chartgen generate --project <name> --tag <tag>`.
6. Add `examples/helmchart-<name>.yaml`. `test/diff` requires one example for each project.
7. Write `docs/<name>.md` and add a row to the [Charts](#charts) table.
8. Add an end-to-end case for the new chart to `test/e2e/run.sh`.
9. Run `go test ./...` and the end-to-end test.

After the merge, the update workflow opens pull requests for new releases of the project, and the publish workflow pushes its charts. After the first publish, set the new package on ghcr.io to public.

## Tests

All tests run in CI on every pull request and before every publish.

| Test | Command | What it does |
| --- | --- | --- |
| Generator | `go test ./internal/...` | Generates charts from small fixtures. Makes sure that layout changes stop the generator. |
| Merge behavior | `go test ./test/merge` | Applies the Helm patch functions to a custom resource with fields that the chart does not render. |
| Diff with upstream | `go test ./test/diff` | Runs `helm lint` and `helm template`. Compares each rendered resource with the upstream manifest. For an operator chart, the allowed differences are the removed Namespace, the `keep` annotation, the hooks, and the custom resource. The custom resource must match the upstream defaults. For a CRD-only chart, the only allowed difference is the `keep` annotation. |
| Regenerate | `go run ./cmd/chartgen regenerate && git diff --exit-code` | Makes sure that `charts/` is the generator output. |
| End-to-end | `test/e2e/run.sh` | Adopts an install that `kubectl apply` made, upgrades it, and uninstalls it on a kind cluster. See the documentation of each chart. |

The diff test downloads the upstream files for each chart version. It needs `helm` on the PATH.

## GitHub Actions

| Workflow | Trigger | Permissions | What it does |
| --- | --- | --- | --- |
| `ci.yaml` | pull request, called by publish, started by update | `contents: read` | All tests. The end-to-end test runs with Helm v4.3.1 (the version in the k3s klipper-helm image) and Helm v3.19.2. |
| `update.yaml` | daily, manual | `contents: read`, then `contents: write`, `pull-requests: write`, and `actions: write` | Finds new stable upstream releases without a chart. Opens one pull request per release and starts `ci.yaml` on its branch. It never publishes. |
| `publish.yaml` | push to `main`, manual | `contents: read`, and `packages: write` plus `id-token: write` only on the publish job | Runs `ci.yaml`. Then pushes each chart version that the registry does not have, and signs it with cosign keyless. |

All actions are pinned by commit SHA.

The publish job never overwrites a published chart version. `hack/publish.sh` runs `helm show chart` for each version first. If the registry answers "not found", the script pushes. Any other error stops the job. A concurrency group makes sure that only one publish runs at a time.

### Repository setup

1. In the repository or organization Actions settings, turn on "Allow GitHub Actions to create and approve pull requests". Without it, `update.yaml` cannot open pull requests. No extra secret is necessary. A pull request that `GITHUB_TOKEN` opens does not start `pull_request` workflows, so `update.yaml` starts `ci.yaml` itself with `workflow_dispatch`. The CI results attach to the head commit of the branch, so they show on the pull request and count for branch protection.
2. ghcr.io creates new packages as private. After the first publish of a chart, set its package (for example `charts/kubevirt`) to public, so that k3s pulls it without credentials.
3. Protect `main`. Make the CI jobs required before merge.

## Verify a chart signature

The publish workflow signs each chart with cosign keyless signing and the GitHub OIDC token. Replace the chart name and version:

```sh
cosign verify ghcr.io/ruddervirt/charts/kubevirt:1.9.1 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/ruddervirt/[^/]+/\.github/workflows/publish\.yaml@refs/heads/main$'
```

## License

Apache-2.0. See `LICENSE`. The charts contain upstream manifests under their own licenses. The KubeVirt and CDI manifests are also Apache-2.0.
