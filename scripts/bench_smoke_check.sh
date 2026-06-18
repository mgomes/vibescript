#!/usr/bin/env bash
set -euo pipefail

threshold_file="${1:-benchmarks/smoke_thresholds.txt}"

if [[ ! -f "$threshold_file" ]]; then
  echo "threshold file not found: $threshold_file" >&2
  exit 2
fi

declare -A max_ns
declare -A max_allocs
declare -A max_bytes
benchmarks=()

while read -r name ns allocs bytes; do
  if [[ -z "${name:-}" || "${name:0:1}" == "#" ]]; then
    continue
  fi
  benchmarks+=("$name")
  max_ns["$name"]="$ns"
  max_allocs["$name"]="$allocs"
  max_bytes["$name"]="${bytes:-}"
done < "$threshold_file"

if [[ ${#benchmarks[@]} -eq 0 ]]; then
  echo "no benchmark thresholds configured in $threshold_file" >&2
  exit 2
fi

pattern="^($(IFS='|'; echo "${benchmarks[*]}"))$"
tmp_out="$(mktemp)"
trap 'rm -f "$tmp_out"' EXIT

go test ./internal/runtime \
  -run '^$' \
  -bench "$pattern" \
  -benchmem \
  -count 3 \
  -benchtime 200ms \
  -cpu 1 | tee "$tmp_out"

declare -A actual_ns
declare -A actual_allocs
declare -A actual_bytes

# With -count 3 we record three samples per benchmark. Keep the best
# (minimum) ns/op so a single noisy run on a shared CI runner does not
# trip the gate; keep the worst (maximum) B/op and allocs/op so allocation
# regressions are not masked by a luckier sample.
while read -r bench ns bytes allocs; do
  actual_ns["$bench"]="$ns"
  actual_bytes["$bench"]="$bytes"
  actual_allocs["$bench"]="$allocs"
done < <(
  awk '
    /^Benchmark/ {
      bench=$1
      sub(/-[0-9]+$/, "", bench)
      ns=""
      bytes=""
      allocs=""
      for (i = 1; i <= NF; i++) {
        if ($i == "ns/op") ns=$(i-1)
        if ($i == "B/op") bytes=$(i-1)
        if ($i == "allocs/op") allocs=$(i-1)
      }
      if (bench == "" || ns == "" || bytes == "" || allocs == "") next
      if (!(bench in min_ns) || ns + 0 < min_ns[bench] + 0) {
        min_ns[bench] = ns
      }
      if (!(bench in max_bytes) || bytes + 0 > max_bytes[bench] + 0) {
        max_bytes[bench] = bytes
      }
      if (!(bench in max_allocs) || allocs + 0 > max_allocs[bench] + 0) {
        max_allocs[bench] = allocs
      }
    }
    END {
      for (bench in min_ns) {
        print bench, min_ns[bench], max_bytes[bench], max_allocs[bench]
      }
    }
  ' "$tmp_out"
)

failures=0

float_delta() {
  awk -v actual="$1" -v limit="$2" 'BEGIN { printf "%.4f", actual - limit }'
}

float_exceeds() {
  awk -v actual="$1" -v limit="$2" 'BEGIN { exit (actual > limit) ? 0 : 1 }'
}

echo
printf "%-40s %12s %12s %12s %12s %12s %12s %12s %12s %14s\n" "Benchmark" "ns/op" "max_ns/op" "delta_ns" "B/op" "max_B/op" "delta_B" "allocs/op" "max_allocs" "delta_allocs"
for bench in "${benchmarks[@]}"; do
  ns="${actual_ns[$bench]:-}"
  bytes="${actual_bytes[$bench]:-}"
  allocs="${actual_allocs[$bench]:-}"
  max_ns_value="${max_ns[$bench]}"
  max_bytes_value="${max_bytes[$bench]:-}"
  max_allocs_value="${max_allocs[$bench]}"

  if [[ -z "$ns" || -z "$bytes" || -z "$allocs" ]]; then
    echo "missing benchmark result for $bench" >&2
    failures=$((failures + 1))
    continue
  fi

  ns_delta="$(float_delta "$ns" "$max_ns_value")"
  bytes_delta=""
  if [[ -n "$max_bytes_value" ]]; then
    bytes_delta="$(float_delta "$bytes" "$max_bytes_value")"
  fi
  allocs_delta="$(float_delta "$allocs" "$max_allocs_value")"
  printf "%-40s %12s %12s %12s %12s %12s %12s %12s %12s %14s\n" "$bench" "$ns" "$max_ns_value" "$ns_delta" "$bytes" "${max_bytes_value:-"-"}" "${bytes_delta:-"-"}" "$allocs" "$max_allocs_value" "$allocs_delta"

  if float_exceeds "$ns" "$max_ns_value"; then
    echo "regression: $bench ns/op $ns exceeds $max_ns_value" >&2
    failures=$((failures + 1))
  fi
  if [[ -n "$max_bytes_value" ]] && float_exceeds "$bytes" "$max_bytes_value"; then
    echo "regression: $bench B/op $bytes exceeds $max_bytes_value" >&2
    failures=$((failures + 1))
  fi
  if float_exceeds "$allocs" "$max_allocs_value"; then
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
