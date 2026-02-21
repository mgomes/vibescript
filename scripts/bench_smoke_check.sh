#!/usr/bin/env bash
set -euo pipefail

threshold_file="${1:-benchmarks/smoke_thresholds.txt}"

if [[ ! -f "$threshold_file" ]]; then
  echo "threshold file not found: $threshold_file" >&2
  exit 2
fi

declare -A max_ns
declare -A max_allocs
benchmarks=()

while read -r name ns allocs; do
  if [[ -z "${name:-}" || "${name:0:1}" == "#" ]]; then
    continue
  fi
  benchmarks+=("$name")
  max_ns["$name"]="$ns"
  max_allocs["$name"]="$allocs"
done < "$threshold_file"

if [[ ${#benchmarks[@]} -eq 0 ]]; then
  echo "no benchmark thresholds configured in $threshold_file" >&2
  exit 2
fi

pattern="^($(IFS='|'; echo "${benchmarks[*]}"))$"
tmp_out="$(mktemp)"
trap 'rm -f "$tmp_out"' EXIT

go test ./vibes \
  -run '^$' \
  -bench "$pattern" \
  -benchmem \
  -count 1 \
  -benchtime 100ms \
  -cpu 1 | tee "$tmp_out"

declare -A actual_ns
declare -A actual_allocs

while read -r bench ns allocs; do
  actual_ns["$bench"]="$ns"
  actual_allocs["$bench"]="$allocs"
done < <(
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
  ' "$tmp_out"
)

failures=0

echo
printf "%-40s %12s %12s %12s %12s %12s %14s\n" "Benchmark" "ns/op" "max_ns/op" "delta_ns" "allocs/op" "max_allocs" "delta_allocs"
for bench in "${benchmarks[@]}"; do
  ns="${actual_ns[$bench]:-}"
  allocs="${actual_allocs[$bench]:-}"
  max_ns_value="${max_ns[$bench]}"
  max_allocs_value="${max_allocs[$bench]}"

  if [[ -z "$ns" || -z "$allocs" ]]; then
    echo "missing benchmark result for $bench" >&2
    failures=$((failures + 1))
    continue
  fi

  ns_delta=$((ns - max_ns_value))
  allocs_delta=$((allocs - max_allocs_value))
  printf "%-40s %12s %12s %12d %12s %12s %14d\n" "$bench" "$ns" "$max_ns_value" "$ns_delta" "$allocs" "$max_allocs_value" "$allocs_delta"

  if (( ns > max_ns_value )); then
    echo "regression: $bench ns/op $ns exceeds $max_ns_value" >&2
    failures=$((failures + 1))
  fi
  if (( allocs > max_allocs_value )); then
    echo "regression: $bench allocs/op $allocs exceeds $max_allocs_value" >&2
    failures=$((failures + 1))
  fi
done

if (( failures > 0 )); then
  echo "benchmark smoke check failed ($failures regression signals)" >&2
  exit 1
fi

echo
echo "benchmark smoke check passed"
