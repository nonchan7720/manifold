#!/bin/bash
# CI 用: このリポジトリを depgraph でスキャンし、warn 以上の Findings（循環 import、層違反、重複、未使用コードなど）
# が 1 件でもあれば失敗する。CLAUDE.md は書き換えない（--claude-md ""）。
#
# depgraph 0.1.5 の scan は Findings があっても exit 0 なので、標準エラーの出力から判定する。
# 出力の形式が変わって判定できなくなったときに黙って通らないよう、毎回出る "scanned N files" の行が
# 無ければ失敗にする。
# TODO: depgraph の `scan --fail-on`（nonchan7720/depgraph#43）がリリースされたら、
#       `depgraph scan --root . --reset --claude-md "" --fail-on warn` に置き換えてこのスクリプトを消す。
set -uo pipefail

out=$(depgraph scan --root . --reset --claude-md "" 2>&1)
status=$?
printf '%s\n' "$out"
if [ "$status" -ne 0 ]; then
  echo "depgraph scan failed (exit $status)" >&2
  exit "$status"
fi
if ! grep -Eq '^scanned [0-9]+ files' <<<"$out"; then
  echo "depgraph: the scan summary was not found in the output; the output format may have changed" >&2
  exit 1
fi
n=$(grep -Ec '^  \[(error|warn)\] ' <<<"$out")
if [ "$n" -gt 0 ]; then
  echo "depgraph: ${n} finding(s) of at least warn" >&2
  exit 1
fi
