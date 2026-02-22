# Benchmark Baselines

This directory stores versioned benchmark baseline artifacts for release
comparison.

## Files

- `v0.20.0-pr.txt`: baseline for PR/push benchmark profile.
  - Generated with: `scripts/bench_runtime.sh --count 1 --benchtime 1s`
- `v0.20.0-full.txt`: baseline for scheduled full benchmark profile.
  - Generated with: `scripts/bench_runtime.sh --count 1 --benchtime 2s`

## Usage

Compare a current run against a baseline:

```bash
scripts/bench_compare_baseline.sh benchmarks/baselines/v0.20.0-pr.txt benchmarks/latest.txt
```

## Updating Baselines

1. Run benchmark profile with stable settings for the target release.
2. Write output to a new versioned file in this directory.
3. Keep prior baseline files for historical comparison.
4. Update workflow/docs references if the active baseline changes.
