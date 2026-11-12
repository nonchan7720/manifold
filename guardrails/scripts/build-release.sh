#!/usr/bin/env bash
# manifold-guardrail のリリース用バイナリを作る。
#   scripts/build-release.sh <version> <出力先ディレクトリ>
# guardrails/ で実行する。OS × アーキテクチャごとにアーカイブと、checksums.txt を出力する。
set -euo pipefail

version="${1:?usage: build-release.sh <version> <output-dir>}"
out="${2:?usage: build-release.sh <version> <output-dir>}"
name="manifold-guardrail"

mkdir -p "${out}"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  goos="${target%/*}"
  goarch="${target#*/}"
  dir="${work}/${goos}_${goarch}"
  mkdir -p "${dir}"
  bin="${name}"
  if [ "${goos}" = "windows" ]; then
    bin="${name}.exe"
  fi
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" go build -trimpath -ldflags="-w -s" -o "${dir}/${bin}" .
  cp README.md README_ja.md "${dir}/"
  archive="${name}_${version}_${goos}_${goarch}"
  if [ "${goos}" = "windows" ]; then
    (cd "${dir}" && zip -q "${out}/${archive}.zip" ./*)
  else
    tar -C "${dir}" -czf "${out}/${archive}.tar.gz" .
  fi
done

(cd "${out}" && sha256sum -- * > checksums.txt)
