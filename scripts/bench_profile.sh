#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: scripts/bench_profile.sh [options]

Runs a benchmark with CPU and memory profiles and writes profile summaries.

options:
  --pattern <regex>   Benchmark regex (default: ^BenchmarkExecutionArrayPipeline$)
  --package <path>    Go package to benchmark (default: ./vibes)
  --benchtime <dur>   Benchtime passed to `go test` (default: 1s)
  --count <n>         Benchmark count (default: 1)
  --cpu <list>        CPU list for `go test -cpu` (default: 1)
  --outdir <path>     Output directory (default: benchmarks/profiles/<timestamp>)
  -h, --help          Show this help
EOF
}

pattern="^BenchmarkExecutionArrayPipeline$"
pkg="./vibes"
benchtime="1s"
count="1"
cpu="1"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
outdir="benchmarks/profiles/${timestamp}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --pattern)
      pattern="${2:-}"
      shift 2
      ;;
    --package)
      pkg="${2:-}"
      shift 2
      ;;
    --benchtime)
      benchtime="${2:-}"
      shift 2
      ;;
    --count)
      count="${2:-}"
      shift 2
      ;;
    --cpu)
      cpu="${2:-}"
      shift 2
      ;;
    --outdir)
      outdir="${2:-}"
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

mkdir -p "$outdir"

bench_out="${outdir}/bench.txt"
cpu_profile="${outdir}/cpu.out"
mem_profile="${outdir}/mem.out"
cpu_top="${outdir}/cpu.top.txt"
mem_top="${outdir}/mem.top.txt"
meta="${outdir}/meta.txt"

command=(
  go test "$pkg"
  -run '^$'
  -bench "$pattern"
  -benchmem
  -count "$count"
  -benchtime "$benchtime"
  -cpu "$cpu"
  -cpuprofile "$cpu_profile"
  -memprofile "$mem_profile"
)

{
  echo "timestamp=${timestamp}"
  echo "pattern=${pattern}"
  echo "package=${pkg}"
  echo "benchtime=${benchtime}"
  echo "count=${count}"
  echo "cpu=${cpu}"
  echo "go_version=$(go version)"
  echo "git_commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
  echo "command=${command[*]}"
} > "$meta"

"${command[@]}" | tee "$bench_out"

go tool pprof -top -nodecount=60 "$cpu_profile" > "$cpu_top"
go tool pprof -top -nodecount=60 "$mem_profile" > "$mem_top"

echo
echo "profile output written to ${outdir}"
echo "  bench: ${bench_out}"
echo "  cpu profile: ${cpu_profile}"
echo "  mem profile: ${mem_profile}"
echo "  cpu top: ${cpu_top}"
echo "  mem top: ${mem_top}"
