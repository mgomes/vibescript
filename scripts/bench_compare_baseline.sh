#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: scripts/bench_compare_baseline.sh <baseline.txt> <current.txt> [--strict]

Compares benchmark outputs and prints per-benchmark trend deltas.

Arguments:
  <baseline.txt>  Baseline benchmark output file
  <current.txt>   Current benchmark output file
  --strict        Fail if benchmarks are missing or new vs baseline
USAGE
}

if [[ $# -lt 2 || $# -gt 3 ]]; then
  usage
  exit 2
fi

baseline_file="$1"
current_file="$2"
strict=0

if [[ $# -eq 3 ]]; then
  if [[ "$3" != "--strict" ]]; then
    usage
    exit 2
  fi
  strict=1
fi

if [[ ! -f "$baseline_file" ]]; then
  echo "baseline file not found: $baseline_file" >&2
  exit 2
fi
if [[ ! -f "$current_file" ]]; then
  echo "current file not found: $current_file" >&2
  exit 2
fi

parse_bench() {
  local file="$1"
  awk '
    /^Benchmark/ {
      bench=$1
      sub(/-[0-9]+$/, "", bench)
      ns=""
      allocs=""
      for (i = 1; i <= NF; i++) {
        if ($i == "ns/op") ns=$(i-1)
        if ($i == "allocs/op") allocs=$(i-1)
      }
      if (bench != "" && ns != "" && allocs != "") {
        print bench, ns, allocs
      }
    }
  ' "$file"
}

declare -A baseline_ns
declare -A baseline_allocs
declare -A current_ns
declare -A current_allocs
benchmarks=()

while read -r bench ns allocs; do
  if [[ -z "${bench:-}" ]]; then
    continue
  fi
  benchmarks+=("$bench")
  baseline_ns["$bench"]="$ns"
  baseline_allocs["$bench"]="$allocs"
done < <(parse_bench "$baseline_file")

if [[ ${#benchmarks[@]} -eq 0 ]]; then
  echo "no benchmark rows found in baseline: $baseline_file" >&2
  exit 2
fi

while read -r bench ns allocs; do
  if [[ -z "${bench:-}" ]]; then
    continue
  fi
  current_ns["$bench"]="$ns"
  current_allocs["$bench"]="$allocs"
done < <(parse_bench "$current_file")

if [[ ${#current_ns[@]} -eq 0 ]]; then
  echo "no benchmark rows found in current file: $current_file" >&2
  exit 2
fi

echo "benchmark baseline compare"
echo "baseline: $baseline_file"
echo "current:  $current_file"
echo
printf "%-40s %12s %12s %12s %12s %12s %12s\n" "Benchmark" "base_ns" "curr_ns" "delta_ns" "base_alloc" "curr_alloc" "delta_alloc"

missing=0
for bench in "${benchmarks[@]}"; do
  base_ns_value="${baseline_ns[$bench]}"
  base_alloc_value="${baseline_allocs[$bench]}"
  curr_ns_value="${current_ns[$bench]:-}"
  curr_alloc_value="${current_allocs[$bench]:-}"

  if [[ -z "$curr_ns_value" || -z "$curr_alloc_value" ]]; then
    printf "%-40s %12s %12s %12s %12s %12s %12s\n" "$bench" "$base_ns_value" "missing" "n/a" "$base_alloc_value" "missing" "n/a"
    missing=$((missing + 1))
    continue
  fi

  ns_delta_pct="$(awk -v base="$base_ns_value" -v curr="$curr_ns_value" 'BEGIN { if (base == 0) { printf "n/a" } else { printf "%+.2f%%", ((curr - base) / base) * 100 } }')"
  alloc_delta_pct="$(awk -v base="$base_alloc_value" -v curr="$curr_alloc_value" 'BEGIN { if (base == 0) { printf "n/a" } else { printf "%+.2f%%", ((curr - base) / base) * 100 } }')"

  printf "%-40s %12s %12s %12s %12s %12s %12s\n" "$bench" "$base_ns_value" "$curr_ns_value" "$ns_delta_pct" "$base_alloc_value" "$curr_alloc_value" "$alloc_delta_pct"
done

extras=()
for bench in "${!current_ns[@]}"; do
  if [[ -z "${baseline_ns[$bench]:-}" ]]; then
    extras+=("$bench")
  fi
done

if [[ ${#extras[@]} -gt 0 ]]; then
  echo
  echo "new benchmarks not present in baseline:"
  printf '%s\n' "${extras[@]}" | sort | sed 's/^/- /'
fi

if (( missing > 0 )); then
  echo
  echo "warning: $missing baseline benchmark(s) missing from current results" >&2
fi

if (( strict == 1 && (missing > 0 || ${#extras[@]} > 0) )); then
  exit 1
fi
