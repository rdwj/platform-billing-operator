#!/usr/bin/env bash
set -euo pipefail

COVER_OUT="${1:-cover.out}"
THRESHOLDS="${2:-hack/coverage-thresholds.txt}"

if [ ! -f "$COVER_OUT" ]; then
  echo "ERROR: $COVER_OUT not found. Run 'make test' first."
  exit 1
fi

FAIL=0

while IFS= read -r line; do
  line="${line%%#*}"
  [[ "$line" =~ ^[[:space:]]*$ ]] && continue

  pkg_suffix="$(echo "$line" | awk '{print $1}')"
  min_pct="$(echo "$line" | awk '{print $2}')"

  [ -z "$pkg_suffix" ] || [ -z "$min_pct" ] && continue

  # Cover profile format: file:start,end numStmts count
  # Match package exactly: /<pkg_suffix>/<filename>.go — no further slashes.
  read -r covered total <<< "$(awk -v pkg="$pkg_suffix" '
    NR == 1 { next }
    {
      # Extract the path after the module prefix up to the .go file.
      # Match: .../<pkg>/somefile.go:... but NOT .../<pkg>/subpkg/...
      split($1, parts, "/")
      # Reconstruct the path minus the filename
      n = length(parts)
      fname = parts[n]
      # Check if this file belongs to exactly this package
      idx = index($1, "/" pkg "/")
      if (idx == 0) next
      # After the pkg/ prefix, there should be a .go filename, not another dir.
      rest = substr($1, idx + length("/" pkg "/"))
      if (index(rest, "/") > 0) next

      numStmts = $2
      count = $3
      total += numStmts
      if (count > 0) covered += numStmts
    }
    END { print covered+0, total+0 }
  ' "$COVER_OUT")"

  if [ "$total" -eq 0 ]; then
    echo "WARN: no coverage data for $pkg_suffix"
    continue
  fi

  actual=$(awk "BEGIN {printf \"%.1f\", ($covered / $total) * 100}")

  if awk "BEGIN {exit !($actual < $min_pct)}"; then
    echo "FAIL: $pkg_suffix coverage ${actual}% < ${min_pct}% minimum"
    FAIL=1
  else
    echo "  OK: $pkg_suffix coverage ${actual}% >= ${min_pct}%"
  fi
done < "$THRESHOLDS"

if [ "$FAIL" -ne 0 ]; then
  echo ""
  echo "Coverage gate FAILED. Update code or thresholds."
  exit 1
fi

echo ""
echo "Coverage gate passed."
