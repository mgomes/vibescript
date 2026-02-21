#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: scripts/bench_runtime.sh [options]

Runs VibeScript Go benchmarks with stable defaults and records output.

options:
  --count <n>        Benchmark count (default: 3)
  --benchtime <dur>  Benchtime passed to `go test` (default: 1s)
  --pattern <regex>  Benchmark regex (default: ^Benchmark)
  --package <path>   Go package to benchmark (default: ./vibes)
  --cpu <list>       CPU list for `go test -cpu` (default: 1)
  --out <file>       Output file (default: benchmarks/latest.txt)
  -h, --help         Show this help
EOF
}

count="3"
benchtime="1s"
pattern="^Benchmark"
pkg="./vibes"
cpu="1"
out_file="benchmarks/latest.txt"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --count)
      count="${2:-}"
      shift 2
      ;;
    --benchtime)
      benchtime="${2:-}"
      shift 2
      ;;
    --pattern)
      pattern="${2:-}"
      shift 2
      ;;
    --package)
      pkg="${2:-}"
      shift 2
      ;;
    --cpu)
      cpu="${2:-}"
      shift 2
      ;;
    --out)
      out_file="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

mkdir -p "$(dirname "$out_file")"

go_version="$(go version)"
git_commit="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

command=(
  go test "$pkg"
  -run '^$'
  -bench "$pattern"
  -benchmem
  -count "$count"
  -benchtime "$benchtime"
  -cpu "$cpu"
)

{
  echo "# VibeScript benchmark run"
  echo "# timestamp: $timestamp"
  echo "# git_commit: $git_commit"
  echo "# go_version: $go_version"
  echo "# package: $pkg"
  echo "# bench_pattern: $pattern"
  echo "# count: $count"
  echo "# benchtime: $benchtime"
  echo "# cpu: $cpu"
  echo "# command: ${command[*]}"
  echo
  "${command[@]}"
} | tee "$out_file"

echo
echo "benchmark output written to $out_file"
