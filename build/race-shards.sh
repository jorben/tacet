#!/usr/bin/env bash
# race-shards.sh DIR BIN N [FLAGS...]
#
# Runs the tests of BIN, a package's test binary (go test -race -c), from the
# package's folder DIR in N processes at once, the tests split round-robin by
# name, and fails if any process fails. FLAGS go to every process.
#
# A package whose tests can't call t.Parallel (they use t.Setenv) runs them one
# at a time and keeps one core busy; internal/app took 417s that way under
# -race on CI. N processes keep N cores busy. Each process makes a test home of
# its own (internal/testenv), so the shards share no files.
set -euo pipefail
dir=$1 bin=$2 n=$3
shift 3
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
(cd "$dir" && "$bin" -test.list '.*') | grep -E '^(Test|Example|Fuzz)' | sort >"$tmp/all"
echo "$(wc -l <"$tmp/all" | tr -d ' ') tests in $n processes"
pids=()
for i in $(seq 0 $((n - 1))); do
	awk -v n="$n" -v i="$i" 'NR % n == i' "$tmp/all" >"$tmp/s$i"
	[ -s "$tmp/s$i" ] || continue
	re="^($(paste -s -d '|' "$tmp/s$i"))\$"
	(cd "$dir" && "$bin" -test.run "$re" "$@" >"$tmp/log$i" 2>&1) &
	pids[i]=$!
done
fail=0
for i in "${!pids[@]}"; do
	if wait "${pids[i]}"; then st=ok; else st=FAIL fail=1; fi
	echo "::group::shard $i: $st ($(wc -l <"$tmp/s$i" | tr -d ' ') tests)"
	cat "$tmp/log$i"
	echo "::endgroup::"
	if [ "$st" = FAIL ]; then grep -E '^(--- FAIL|FAIL|panic|WARNING: DATA RACE)' "$tmp/log$i" || true; fi
done
exit $fail
