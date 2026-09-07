#!/usr/bin/env bash
# tool-install-launchd.test.sh — hermetic over a scratch $HOME and a fake
# $BD_COUNTS_PROJECTS_DIR. launchctl is stubbed on PATH (it only ever logs its
# argv), so this never touches the real launchd or the real LaunchAgents dir.
#
# Run:  bash scripts/tests/tool-install-launchd.test.sh
set -uo pipefail
SUT="$(cd "$(dirname "$0")/.." && pwd)/tool-install-launchd.sh"

pass=0; fail=0
ok()  { pass=$((pass+1)); printf '  ✓ %s\n' "$1"; }
bad() { fail=$((fail+1)); printf '  ✗ %s — %s\n' "$1" "$2"; }
has()    { case "$2" in *"$1"*) ok "$3" ;; *) bad "$3" "missing [$1]" ;; esac; }
lacks()  { case "$2" in *"$1"*) bad "$3" "found forbidden [$1]" ;; *) ok "$3" ;; esac; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
TMP="$(cd "$TMP" && pwd -P)"   # canonicalize: macOS /var -> /private/var
FHOME="$TMP/home"
PROJDIR="$TMP/projects"
mkdir -p "$FHOME" "$PROJDIR/repo-a/.beads" "$PROJDIR/repo-b/.beads" \
         "$PROJDIR/no-beads-dir" "$PROJDIR/empty-beads/.beads"
touch "$PROJDIR/repo-a/.beads/last-touched"
touch "$PROJDIR/repo-b/.beads/metadata.json"
# empty-beads/.beads exists but carries neither trigger file — must be excluded.
# no-beads-dir has no .beads at all — must be excluded.

NEW_PLIST="$FHOME/Library/LaunchAgents/com.trixi.beadwatch.plist"
OLD_PLIST="$FHOME/Library/LaunchAgents/com.trixi.bd-counts.plist"
LCLOG="$TMP/launchctl.log"
UIDNUM="$(id -u)"

STUBBIN="$TMP/stubbin"; mkdir -p "$STUBBIN"
printf '#!/bin/sh\necho "$@" >> "%s"\nexit 0\n' "$LCLOG" > "$STUBBIN/launchctl"
chmod +x "$STUBBIN/launchctl"

# The SUT refuses to author a plist naming a binary that is not there, so the
# scratch home needs one at each GOBIN this file exercises.
mkdir -p "$FHOME/go/bin" "$TMP/altbin"
printf '#!/bin/sh\nexit 0\n' > "$FHOME/go/bin/beadwatch"; chmod +x "$FHOME/go/bin/beadwatch"
printf '#!/bin/sh\nexit 0\n' > "$TMP/altbin/beadwatch";   chmod +x "$TMP/altbin/beadwatch"

# GOBIN is pinned empty rather than inherited: the SUT reads `go env GOBIN`, and
# a developer machine that sets it (dk's second laptop uses /usr/local/bin) would
# otherwise move the binary path out from under every assertion below.
run() {
  HOME="$FHOME" BD_COUNTS_PROJECTS_DIR="$PROJDIR" PATH="$STUBBIN:$PATH" GOBIN="" \
    bash "$SUT" "$@" 2>&1
}

# run_gobin <dir> [args...] — same, with GOBIN pointed somewhere else.
run_gobin() {
  _gb="$1"; shift
  HOME="$FHOME" BD_COUNTS_PROJECTS_DIR="$PROJDIR" PATH="$STUBBIN:$PATH" GOBIN="$_gb" \
    bash "$SUT" "$@" 2>&1
}

echo "--check on a bare machine reports everything to do:"
OUT="$(run --check)"; RC=$?
[ "$RC" -eq 1 ] && ok "--check exits 1 before install" || bad "--check exits 1 before install" "got $RC"
has "$PROJDIR/repo-a" "$OUT" "prints repo-a in the repo list it would watch"
has "$PROJDIR/repo-b" "$OUT" "prints repo-b in the repo list it would watch"
lacks "no-beads-dir" "$OUT" "a dir with no .beads is never listed"
lacks "empty-beads" "$OUT" "a .beads dir with neither trigger file is never listed"
has "missing or stale" "$OUT" "names the plist as not yet current"

echo "install writes the plist and loads it:"
OUT2="$(run)"; RC2=$?
[ "$RC2" -eq 0 ] && ok "install exits 0" || bad "install exits 0" "got $RC2: $OUT2"
[ -f "$NEW_PLIST" ] && ok "com.trixi.beadwatch.plist written" || bad "plist written" "absent"
CONTENT="$(cat "$NEW_PLIST" 2>/dev/null)"
has "<string>com.trixi.beadwatch</string>" "$CONTENT" "Label is com.trixi.beadwatch"
has "<string>$FHOME/go/bin/beadwatch</string>" "$CONTENT" "ProgramArguments names the beadwatch binary"
PA_BLOCK="$(awk '/<key>ProgramArguments<\/key>/{f=1} f{print} f && /<\/array>/{exit}' "$NEW_PLIST")"
PA_COUNT="$(grep -c '<string>' <<<"$PA_BLOCK")"
[ "$PA_COUNT" -eq 1 ] && ok "ProgramArguments has exactly one string (no arguments)" \
                       || bad "ProgramArguments has exactly one string" "found $PA_COUNT"
lacks "--all" "$PA_BLOCK" "ProgramArguments never carries --all — bare invocation is changed-only mode"
has "<integer>120</integer>" "$CONTENT" "StartInterval is 120"
has "<integer>2</integer>" "$CONTENT" "ThrottleInterval is 2"
has "$FHOME/.cache/cc-dashboard/beadwatch.log" "$CONTENT" "StandardErrorPath is the beadwatch log"
has "/opt/homebrew/bin:/usr/local/bin:$FHOME/go/bin:/usr/bin:/bin" "$CONTENT" \
    "EnvironmentVariables PATH carries homebrew, go/bin and the HOME-expanded path"
lacks "$FHOME/go/bin:/opt/homebrew" "$CONTENT" \
    "an empty GOBIN never prepends a duplicate go/bin to PATH"
has "<key>BD_COUNTS_PROJECTS_DIR</key>" "$CONTENT" "EnvironmentVariables carries the BD_COUNTS_PROJECTS_DIR key"
has "<string>$PROJDIR</string>" "$CONTENT" "BD_COUNTS_PROJECTS_DIR is set to the discovered projects dir"
lacks '<string>$HOME' "$CONTENT" "PATH is expanded at write time, not left as a literal \$HOME for launchd"
has "$PROJDIR/repo-a/.beads/last-touched" "$CONTENT" "WatchPaths carries repo-a's real marker (last-touched)"
has "$PROJDIR/repo-b/.beads/metadata.json" "$CONTENT" "WatchPaths carries repo-b's real marker (metadata.json, since repo-b has no last-touched)"
WPN="$(awk '/<key>WatchPaths<\/key>/{f=1;next} f && /<\/array>/{exit} f && /<string>/' "$NEW_PLIST" | grep -c '<string>')"
[ "$WPN" -eq 2 ] && ok "WatchPaths has exactly one entry per discovered repo, nothing else" \
                  || bad "WatchPaths entry count" "found $WPN, want 2"

echo "install loads the new agent through the stubbed launchctl:"
has "bootstrap gui/$UIDNUM $NEW_PLIST" "$(cat "$LCLOG")" "launchctl bootstrap was called for the new plist"

echo "--check is clean once installed:"
OUT3="$(run --check)"; RC3=$?
[ "$RC3" -eq 0 ] && ok "--check exits 0 after install" || bad "--check exits 0 after install" "got $RC3: $OUT3"

echo "a second install rewrites nothing (idempotent):"
HASH1="$(shasum "$NEW_PLIST" | awk '{print $1}')"
LCLOG_LINES1="$(wc -l < "$LCLOG" | tr -d ' ')"
OUT4="$(run)"; RC4=$?
[ "$RC4" -eq 0 ] && ok "second install exits 0" || bad "second install exits 0" "got $RC4: $OUT4"
HASH2="$(shasum "$NEW_PLIST" | awk '{print $1}')"
[ "$HASH1" = "$HASH2" ] && ok "plist content unchanged after a second install" \
                        || bad "plist content unchanged" "content changed"
LCLOG_LINES2="$(wc -l < "$LCLOG" | tr -d ' ')"
[ "$LCLOG_LINES1" -eq "$LCLOG_LINES2" ] && ok "a no-op second install never calls launchctl again" \
                                        || bad "no-op install calls launchctl" \
                                               "log grew from $LCLOG_LINES1 to $LCLOG_LINES2 lines"

echo "old com.trixi.bd-counts is unloaded and removed:"
# Seed it fresh (the earlier installs above ran against a machine that never
# had one) with the Label this script is allowed to retire.
cat > "$OLD_PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.trixi.bd-counts</string>
</dict>
</plist>
PLIST
OUT5="$(run --check)"; RC5=$?
[ "$RC5" -eq 1 ] && ok "--check exits 1 while the old agent is still present" || bad "--check sees the old agent" "got $RC5"
has "still present" "$OUT5" "--check names the old agent as still present"
OUT6="$(run)"; RC6=$?
[ "$RC6" -eq 0 ] && ok "install (with old agent present) exits 0" || bad "install exits 0" "got $RC6: $OUT6"
[ -f "$OLD_PLIST" ] && bad "com.trixi.bd-counts.plist removed" "still present" \
                     || ok "com.trixi.bd-counts.plist removed"
has "bootout gui/$UIDNUM/com.trixi.bd-counts" "$(cat "$LCLOG")" "launchctl bootout was called for the old agent"

echo "a plist with a foreign Label at the new path is refused:"
printf '<?xml version="1.0" encoding="UTF-8"?>\n<plist version="1.0"><dict><key>Label</key><string>com.someone.else</string></dict></plist>\n' > "$NEW_PLIST"
FOREIGN_BEFORE="$(cat "$NEW_PLIST")"
OUT7="$(run --check)"; RC7=$?
[ "$RC7" -eq 1 ] && ok "--check exits 1 on a foreign Label" || bad "--check exits 1 on a foreign Label" "got $RC7"
has "com.someone.else" "$OUT7" "--check names the foreign Label it found"
has "refusing to touch" "$OUT7" "--check says it is refusing to touch the file"
LCLOG_LINES3="$(wc -l < "$LCLOG" | tr -d ' ')"
OUT8="$(run)"; RC8=$?
[ "$RC8" -eq 1 ] && ok "install exits 1 rather than overwrite a foreign plist" || bad "install refuses a foreign plist" "got $RC8"
[ "$(cat "$NEW_PLIST")" = "$FOREIGN_BEFORE" ] && ok "the foreign plist's content is untouched" \
                                              || bad "foreign plist untouched" "content changed"
LCLOG_LINES4="$(wc -l < "$LCLOG" | tr -d ' ')"
[ "$LCLOG_LINES3" -eq "$LCLOG_LINES4" ] && ok "launchctl is never called against a refused foreign plist" \
                                        || bad "launchctl not called on refusal" \
                                               "log grew from $LCLOG_LINES3 to $LCLOG_LINES4 lines"
rm -f "$NEW_PLIST"; run >/dev/null 2>&1   # repair for the next block

echo "a plist with a foreign Label at the old path is refused:"
printf '<?xml version="1.0" encoding="UTF-8"?>\n<plist version="1.0"><dict><key>Label</key><string>com.someone.else</string></dict></plist>\n' > "$OLD_PLIST"
OUT9="$(run --check)"; RC9=$?
[ "$RC9" -eq 1 ] && ok "--check exits 1 on a foreign Label at the old path" || bad "--check exits 1" "got $RC9"
has "com.someone.else" "$OUT9" "--check names the foreign Label at the old path"
has "refusing to remove" "$OUT9" "--check says it is refusing to remove the file"
run >/dev/null 2>&1
[ -f "$OLD_PLIST" ] && ok "a foreign plist at the old path survives a real install run" \
                     || bad "foreign old plist survives" "it was removed"
rm -f "$OLD_PLIST"

echo "a set GOBIN moves the binary path, and PATH follows it:"
# bw-q62: dk's second laptop sets GOBIN=/usr/local/bin, so `go install` puts
# beadwatch there and a hardcoded ~/go/bin names nothing. launchd's failure is
# silent — every 120s, with counts.json simply never written.
rm -f "$NEW_PLIST"
OUT10="$(run_gobin "$TMP/altbin")"; RC10=$?
[ "$RC10" -eq 0 ] && ok "install exits 0 with GOBIN set" || bad "install exits 0 with GOBIN set" "got $RC10: $OUT10"
CONTENT_G="$(cat "$NEW_PLIST" 2>/dev/null)"
has "<string>$TMP/altbin/beadwatch</string>" "$CONTENT_G" "ProgramArguments names the binary under GOBIN"
lacks "<string>$FHOME/go/bin/beadwatch</string>" "$CONTENT_G" "the hardcoded ~/go/bin path is gone"
has "$TMP/altbin:/opt/homebrew/bin" "$CONTENT_G" "GOBIN is prepended to the agent's PATH"

echo "install refuses when the resolved binary is absent:"
rm -f "$NEW_PLIST"
OUT11="$(run_gobin "$TMP/nowhere")"; RC11=$?
[ "$RC11" -eq 2 ] && ok "install exits 2 when the binary is missing" || bad "install exits 2 when the binary is missing" "got $RC11"
has "no beadwatch binary at $TMP/nowhere/beadwatch" "$OUT11" "the refusal names the path it looked at"
[ -f "$NEW_PLIST" ] && bad "no plist is written on refusal" "one was written" \
                     || ok "no plist is written on refusal"
OUT12="$(run_gobin "$TMP/nowhere" --check)"; RC12=$?
[ "$RC12" -eq 1 ] && ok "--check still reports rather than refusing" || bad "--check still reports" "got $RC12: $OUT12"

echo "a config.yaml-only repo (loto's shape) is discovered and watched (sd-3wp.15):"
mkdir -p "$PROJDIR/config-only/.beads"
: > "$PROJDIR/config-only/.beads/config.yaml"   # no last-touched, no metadata.json
rm -f "$NEW_PLIST"
OUT13="$(run)"; RC13=$?
[ "$RC13" -eq 0 ] && ok "install exits 0 with a config.yaml-only repo present" \
                   || bad "install exits 0" "got $RC13: $OUT13"
CONTENT_CO="$(cat "$NEW_PLIST" 2>/dev/null)"
has "$PROJDIR/config-only" "$OUT13" "prints config-only in the repo list it would watch"
has "$PROJDIR/config-only/.beads/config.yaml" "$CONTENT_CO" \
    "WatchPaths carries config-only's real marker (config.yaml)"

echo "BD_COUNTS_EXTRA_REPOS adds a repo outside PROJECTS_DIR entirely (chezmoi's shape, sd-3wp.15):"
EXTRA="$TMP/extra-repo"
mkdir -p "$EXTRA/.beads"
: > "$EXTRA/.beads/metadata.json"
MISSING_EXTRA="$TMP/does-not-exist"
rm -f "$NEW_PLIST"
OUT14="$(HOME="$FHOME" BD_COUNTS_PROJECTS_DIR="$PROJDIR" BD_COUNTS_EXTRA_REPOS="$EXTRA:$MISSING_EXTRA" \
         PATH="$STUBBIN:$PATH" GOBIN="" bash "$SUT" 2>&1)"; RC14=$?
[ "$RC14" -eq 0 ] && ok "install exits 0 with BD_COUNTS_EXTRA_REPOS set" \
                   || bad "install exits 0" "got $RC14: $OUT14"
has "$EXTRA" "$OUT14" "prints the extra repo in the repo list it would watch"
lacks "$MISSING_EXTRA" "$OUT14" "a non-repo path in BD_COUNTS_EXTRA_REPOS is dropped, not watched"
CONTENT_EX="$(cat "$NEW_PLIST" 2>/dev/null)"
has "$EXTRA/.beads/metadata.json" "$CONTENT_EX" "WatchPaths carries the extra repo's marker"
has "<key>BD_COUNTS_EXTRA_REPOS</key>" "$CONTENT_EX" "EnvironmentVariables carries BD_COUNTS_EXTRA_REPOS"
has "<string>$EXTRA:$MISSING_EXTRA</string>" "$CONTENT_EX" \
    "BD_COUNTS_EXTRA_REPOS is passed through verbatim so the beadwatch process re-checks it itself"

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
