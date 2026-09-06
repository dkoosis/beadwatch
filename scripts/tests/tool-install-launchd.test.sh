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

run() {
  HOME="$FHOME" BD_COUNTS_PROJECTS_DIR="$PROJDIR" PATH="$STUBBIN:$PATH" \
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
has "<key>BD_COUNTS_PROJECTS_DIR</key>" "$CONTENT" "EnvironmentVariables carries the BD_COUNTS_PROJECTS_DIR key"
has "<string>$PROJDIR</string>" "$CONTENT" "BD_COUNTS_PROJECTS_DIR is set to the discovered projects dir"
lacks '<string>$HOME' "$CONTENT" "PATH is expanded at write time, not left as a literal \$HOME for launchd"
has "$PROJDIR/repo-a/.beads/last-touched" "$CONTENT" "WatchPaths carries repo-a"
has "$PROJDIR/repo-b/.beads/last-touched" "$CONTENT" "WatchPaths carries repo-b"
WPN="$(grep -c '.beads/last-touched</string>' "$NEW_PLIST" 2>/dev/null || echo 0)"
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

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
