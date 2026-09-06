#!/usr/bin/env sh
# lint-commit-subject.sh — THE conventional-commit rule. One implementation.
#
# WHY THIS FILE EXISTS (sd-edki). The rule used to be written out five times:
# githooks/commit-msg, .beads/hooks/commit-msg, the installed copy under the
# global core.hooksPath, .github/workflows/gate.yml, and
# plugins/sdlc/scripts/tool-ship.sh. Five copies are five sources of truth, and
# the failure they produce is the one that costs a round trip — a subject the
# local hook accepts and CI rejects, or the reverse. Every enforcement point now
# calls this script; nothing else states the vocabulary or the regex.
#
# Installed GLOBALLY beside the hooks via core.hooksPath (scripts/tool-install-hooks.sh),
# so githooks/commit-msg finds it as a sibling of $0 in any repo.
#
# Usage:
#   lint-commit-subject.sh --file <commit-msg-file>   # extract, then validate
#   lint-commit-subject.sh --subject "<text>"         # validate text directly
#   lint-commit-subject.sh --types                    # print the type list
#
# Exit: 0 accepted (or exempt) · 1 rejected · 2 called wrong.
set -eu

TYPES='feat fix docs chore refactor test perf'
# The scope is bounded to non-parens so `feat(a)b): x` is rejected rather than
# swallowed by a greedy `.+`. No attempt at the full Conventional Commits
# grammar — the invariant we want is "a known type prefix is present".
PATTERN='^(feat|fix|docs|chore|refactor|test|perf)(\([^()]+\))?!?: .'

usage() {
  printf 'usage: %s --file <commit-msg-file> | --subject <text> | --types\n' "$(basename "$0")" >&2
  exit 2
}

mode=''; arg=''
case "${1:-}" in
  --types)   printf '%s\n' "$TYPES"; exit 0 ;;
  --file)    mode=file;    arg="${2:-}" ;;
  --subject) mode=subject; arg="${2:-}" ;;
  *)         usage ;;
esac
[ -n "$arg" ] || usage

if [ "$mode" = file ]; then
  [ -f "$arg" ] || { printf '✗ lint-commit-subject: no such file: %s\n' "$arg" >&2; exit 2; }
  # The first line that is neither blank nor a comment — NOT literally the first
  # line, because git leaves its template comments in the file at commit-msg
  # time. The comment character is configurable (core.commentChar); `auto` picks
  # one at cleanup time from a candidate set, and '#' is what it lands on for
  # every message that does not already start a line with '#', so '#' is the
  # honest default rather than the hardcoded assumption it replaced.
  cc="$(git config --get core.commentChar 2>/dev/null || true)"
  { [ -n "$cc" ] && [ "$cc" != auto ]; } || cc='#'
  # awk, not grep: cc is data here, and feeding it to a regex would break the
  # moment someone sets core.commentChar to a metacharacter.
  subject="$(awk -v cc="$cc" '
    { line = $0
      sub(/^[ \t]+/, "", line)
      if (line == "") next
      if (substr(line, 1, length(cc)) == cc) next
      print line; exit }
  ' "$arg")"
else
  subject="$arg"
fi

# Allow standard merge/revert/autosquash subjects. ‡ This is a TEXT test, not a
# provenance test: `git commit -m "Merge whatever I want"` passes it too. That is
# acceptable — a commit-message convention is a guardrail, not a security
# boundary — but the comment must not claim more than the code establishes.
case "$subject" in
  "Merge "*|"Revert "*|"fixup! "*|"squash! "*|"amend! "*) exit 0 ;;
esac

if printf '%s' "$subject" | grep -Eq "$PATTERN"; then
  exit 0
fi

cat >&2 <<EOF
✗ commit rejected: subject must start with a conventional-commit type.

  got:      $subject
  expected: <type>[(scope)][!]: <summary>
  types:    $TYPES

  bead-type -> commit-type:  bug->fix  feature->feat  chore->chore
EOF
exit 1
