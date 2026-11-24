#!/usr/bin/env bash
# Packages every chart under charts/, pushes the versions that the registry does
# not have yet, and signs them with cosign. It never overwrites a published version.
#
# Environment:
#   REGISTRY     OCI repository prefix (default: ghcr.io/ruddervirt/charts)
#   SIGN         1 to sign with cosign keyless (default: 1)
#   PLAIN_HTTP   1 to use plain HTTP (local test registry only)
#   DRY_RUN      1 to print what would be pushed, without pushing
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
REGISTRY=${REGISTRY:-ghcr.io/ruddervirt/charts}
SIGN=${SIGN:-1}
HELM=${HELM:-helm}
COSIGN=${COSIGN:-cosign}
http_args=()
[[ "${PLAIN_HTTP:-}" == 1 ]] && http_args=(--plain-http)

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

field() { sed -n "s/^$1: //p" "$2"; }

mapfile -t charts < <(find "$ROOT/charts" -mindepth 3 -maxdepth 3 -name Chart.yaml | sort -V)
if [[ ${#charts[@]} -eq 0 ]]; then
  echo "no charts found" >&2
  exit 1
fi

for chart_yaml in "${charts[@]}"; do
  dir=$(dirname "$chart_yaml")
  name=$(field name "$chart_yaml")
  version=$(field version "$chart_yaml")
  ref="oci://$REGISTRY/$name"

  # Exists already? "helm show chart" fails with "not found" for a missing tag.
  # Any other error stops the script, so a registry problem never leads to a push.
  if msg=$($HELM show chart "$ref" --version "$version" "${http_args[@]}" 2>&1); then
    echo "skip $name $version: already published"
    continue
  fi
  if ! grep -qiE 'not found|manifest unknown|name unknown' <<<"$msg"; then
    echo "cannot check whether $name $version exists:" >&2
    echo "$msg" >&2
    exit 1
  fi

  if [[ "${DRY_RUN:-}" == 1 ]]; then
    echo "would push $name $version"
    continue
  fi

  $HELM package "$dir" --destination "$out" >/dev/null
  pkg="$out/$name-$version.tgz"
  push_out=$($HELM push "$pkg" "oci://$REGISTRY" "${http_args[@]}" 2>&1)
  echo "$push_out"
  digest=$(awk '/^Digest:/ {print $2}' <<<"$push_out")
  if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "no digest in helm push output" >&2
    exit 1
  fi
  echo "pushed $name $version $digest"

  if [[ "$SIGN" == 1 ]]; then
    $COSIGN sign --yes "$REGISTRY/$name@$digest"
    echo "signed $REGISTRY/$name@$digest"
  fi
done
