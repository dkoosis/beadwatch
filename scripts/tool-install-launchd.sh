#!/usr/bin/env bash
# tool-install-launchd.sh — author ~/Library/LaunchAgents/com.trixi.beadwatch.plist
# from discovered repos, load it, and retire com.trixi.bd-counts (bw-lr1).
#
# WHY: beadwatch replaces strand as the writer of counts.json (design:
# ~/Projects/kg/Project/beadwatch/specs/beadwatch-design.md, decision nug
# 477486825755). The plist that wakes it needs a WatchPaths entry per repo
# with a .beads dir — a list that changes as repos come and go — so it is
# authored here rather than hand-maintained. Pattern and --check/repair split
# copied from sdlc's scripts/tool-install-hooks.sh.
#
# What it does, idempotent:
#   1. Discovers every directory directly under $BD_COUNTS_PROJECTS_DIR
#      (default ~/Projects) holding .beads/last-touched, .beads/metadata.json,
#      or a bare .beads/config.yaml (a server/remote-backed store — loto's
#      shape: no local store file is ever written there, sd-3wp.15) — plus
#      any repo named in $BD_COUNTS_EXTRA_REPOS (colon-separated repo roots,
#      each checked before being watched) for a bd repo that lives outside
#      PROJECTS_DIR's one-level scan entirely (chezmoi's dotfiles checkout at
#      ~/.local/share/chezmoi is the known case).
#   2. Authors com.trixi.beadwatch.plist: ProgramArguments ~/go/bin/beadwatch
#      with NO arguments — bare invocation is beadwatch's changed-only mode
#      (internal/counts/refresh.go's modeChanged: skips any repo whose
#      .beads mtime is unchanged). A WatchPaths wake fires with --all's
#      unconditional sweep of every discovered repo measured at ~2.4 min on
#      this machine's 23 repos — too slow for a bd write to reach counts.json
#      inside the 10s AC, and the wrong mode for a per-repo wake signal
#      anyway. EnvironmentVariables PATH (launchd's own PATH has no homebrew or
#      go bin dir, and bd lives in one of those), BD_COUNTS_PROJECTS_DIR, and
#      BD_COUNTS_EXTRA_REPOS when set (so the beadwatch process's own
#      discover() re-checks the same extra repos every run, not just at
#      install time) — one WatchPaths entry per discovered repo's real marker
#      file (last-touched, else metadata.json, else config.yaml),
#      StartInterval 120, ThrottleInterval 2, RunAtLoad, stderr to
#      ~/.cache/cc-dashboard/beadwatch.log. Rewrites the file only when its
#      content would change — in practice that means only when the discovered
#      repo set changed, since everything else here is fixed. (bootout+bootstrap
#      only fire on an actual rewrite, so a no-op run never touches launchd.)
#   3. Unloads and removes com.trixi.bd-counts.plist, the strand-era agent this
#      one replaces.
#   4. Refuses to touch either plist path if a file already sits there with a
#      Label that isn't the one this script owns for that path — a foreign
#      file at either path is not ours to overwrite or delete.
#
# Usage:
#   bash scripts/tool-install-launchd.sh            # install / repair
#   bash scripts/tool-install-launchd.sh --check     # report only; exit 1 if anything to do
#
# ‡ HOME is read via the environment, never hardcoded, so a test can run this
# against a scratch HOME. launchctl is invoked by bare name (resolved through
# PATH), never by an absolute path, so a test can stub it there.
set -euo pipefail

HOME_DIR="${HOME:?HOME must be set}"
PROJECTS_DIR="${BD_COUNTS_PROJECTS_DIR:-$HOME_DIR/Projects}"
LAUNCH_AGENTS_DIR="$HOME_DIR/Library/LaunchAgents"
NEW_LABEL="com.trixi.beadwatch"
OLD_LABEL="com.trixi.bd-counts"
NEW_PLIST="$LAUNCH_AGENTS_DIR/$NEW_LABEL.plist"
OLD_PLIST="$LAUNCH_AGENTS_DIR/$OLD_LABEL.plist"
# Go installs to GOBIN when it is set, and only otherwise to $HOME/go/bin. dk's
# second laptop sets GOBIN=/usr/local/bin (dotfiles 432305a converged user-
# installed utilities there), so a hardcoded ~/go/bin names a binary that is not
# on that machine — and launchd then fails silently every 120s, leaving
# counts.json unwritten with nothing on screen to say why (bw-q62). `go` can be
# absent from a stripped PATH, so both legs fall back.
GOBIN_DIR="$(command -v go >/dev/null 2>&1 && go env GOBIN 2>/dev/null || true)"
[ -n "$GOBIN_DIR" ] || GOBIN_DIR="$HOME_DIR/go/bin"
BEADWATCH_BIN="$GOBIN_DIR/beadwatch"
LOG_PATH="$HOME_DIR/.cache/cc-dashboard/beadwatch.log"
UID_NUM="$(id -u)"
# launchd's own PATH is /usr/bin:/bin:/usr/sbin:/sbin — bd lives at
# /opt/homebrew/bin/bd, so beadwatch cannot find it without this. $HOME_DIR is
# expanded here, at write time, into a literal path — not left as "$HOME" for
# launchd to expand, since launchd does not expand it.
AGENT_PATH="/opt/homebrew/bin:/usr/local/bin:$HOME_DIR/go/bin:/usr/bin:/bin"
# A GOBIN outside that list would leave the agent unable to reach its own
# binary's siblings. Prepend it only when it is not already covered, so the
# common empty-GOBIN case writes the same string it always did.
case ":$AGENT_PATH:" in
  *":$GOBIN_DIR:"*) ;;
  *) AGENT_PATH="$GOBIN_DIR:$AGENT_PATH" ;;
esac

CHECK=0
while [ $# -gt 0 ]; do
  case "$1" in
    --check) CHECK=1 ;;
    *) echo "tool-install-launchd: unknown argument '$1'" >&2; exit 2 ;;
  esac
  shift
done

missing=0
ok()   { printf '  ✓ %s\n' "$1"; }
todo() { printf '  ✗ %s\n' "$1"; missing=$((missing+1)); }

command -v plutil    >/dev/null 2>&1 || { echo "tool-install-launchd: plutil required"    >&2; exit 2; }
command -v launchctl >/dev/null 2>&1 || { echo "tool-install-launchd: launchctl required" >&2; exit 2; }

# Refuse to author a plist naming a binary that is not there — that is the
# silent-every-120s failure of bw-q62, and it costs nothing to catch here.
# --check is exempt: its job is to report what is missing, not to require it.
if [ "$CHECK" -eq 0 ] && [ ! -x "$BEADWATCH_BIN" ]; then
  echo "tool-install-launchd: no beadwatch binary at $BEADWATCH_BIN — run 'make install' first" >&2
  exit 2
fi

# --- discover repos ----------------------------------------------------------
# Direct children only — matches the shape of the bd-counts template plist
# this replaces (one WatchPaths entry per top-level ~/Projects dir). A repo is
# any dir carrying .beads/last-touched, .beads/metadata.json (both the
# embedded-Dolt shapes), OR .beads/config.yaml (a server/remote-backed store —
# loto's shape: no local store file is ever written there, sd-3wp.15) —
# matching internal/counts/refresh.go's isBeadsRepo.
is_beads_repo() {
  [ -f "$1/.beads/last-touched" ] || [ -f "$1/.beads/metadata.json" ] || [ -f "$1/.beads/config.yaml" ]
}
# repo_marker <dir> — the one existing marker file under <dir>/.beads, in the
# same preference order as is_beads_repo, for a WatchPaths entry that actually
# exists on disk.
repo_marker() {
  if   [ -f "$1/.beads/last-touched" ]; then printf '%s\n' "$1/.beads/last-touched"
  elif [ -f "$1/.beads/metadata.json" ]; then printf '%s\n' "$1/.beads/metadata.json"
  else printf '%s\n' "$1/.beads/config.yaml"
  fi
}
discover_repos() {
  [ -d "$PROJECTS_DIR" ] || return 0
  find "$PROJECTS_DIR" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | while IFS= read -r d; do
    is_beads_repo "$d" && printf '%s\n' "$d"
  done | sort
}
REPOS=()
while IFS= read -r _r; do
  [ -n "$_r" ] && REPOS+=("$_r")
done < <(discover_repos)
# Repos outside PROJECTS_DIR's one-level scan entirely — chezmoi's dotfiles
# checkout at ~/.local/share/chezmoi is the known case (sd-3wp.15). Named via
# BD_COUNTS_EXTRA_REPOS (colon-separated repo roots, each checked before being
# added so a stale/mistyped entry is dropped rather than watched as broken).
EXTRA_REPOS_RAW="${BD_COUNTS_EXTRA_REPOS:-}"
if [ -n "$EXTRA_REPOS_RAW" ]; then
  _old_ifs="$IFS"; IFS=':'
  for _er in $EXTRA_REPOS_RAW; do
    IFS="$_old_ifs"
    [ -n "$_er" ] || continue
    is_beads_repo "$_er" && REPOS+=("$_er")
  done
  IFS="$_old_ifs"
fi

echo "repos to watch (under $PROJECTS_DIR):"
if [ "${#REPOS[@]}" -eq 0 ]; then
  echo "  (none found)"
else
  for _r in "${REPOS[@]}"; do printf '  - %s\n' "$_r"; done
fi

# plist_label <file> — the Label key, or empty if the file is absent/unreadable.
plist_label() {
  [ -f "$1" ] || return 0
  plutil -extract Label raw -o - "$1" 2>/dev/null || true
}

# build_new_plist <repo>... — the exact bytes this script wants at $NEW_PLIST.
build_new_plist() {
  printf '<?xml version="1.0" encoding="UTF-8"?>\n'
  printf '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n'
  printf '<plist version="1.0">\n<dict>\n'
  printf '    <key>Label</key>\n    <string>%s</string>\n' "$NEW_LABEL"
  printf '    <key>ProgramArguments</key>\n    <array>\n'
  printf '        <string>%s</string>\n' "$BEADWATCH_BIN"
  printf '    </array>\n'
  printf '    <key>EnvironmentVariables</key>\n    <dict>\n'
  printf '        <key>PATH</key>\n        <string>%s</string>\n' "$AGENT_PATH"
  printf '        <key>BD_COUNTS_PROJECTS_DIR</key>\n        <string>%s</string>\n' "$PROJECTS_DIR"
  if [ -n "$EXTRA_REPOS_RAW" ]; then
    printf '        <key>BD_COUNTS_EXTRA_REPOS</key>\n        <string>%s</string>\n' "$EXTRA_REPOS_RAW"
  fi
  printf '    </dict>\n'
  printf '    <key>WatchPaths</key>\n    <array>\n'
  for _r in "$@"; do
    printf '        <string>%s</string>\n' "$(repo_marker "$_r")"
  done
  printf '    </array>\n'
  printf '    <key>StartInterval</key>\n    <integer>120</integer>\n'
  printf '    <key>RunAtLoad</key>\n    <true/>\n'
  printf '    <key>StandardErrorPath</key>\n    <string>%s</string>\n' "$LOG_PATH"
  printf '    <key>ThrottleInterval</key>\n    <integer>2</integer>\n'
  printf '</dict>\n</plist>\n'
}

if [ "${#REPOS[@]}" -gt 0 ]; then
  desired="$(build_new_plist "${REPOS[@]}")"
else
  desired="$(build_new_plist)"
fi

# --- 1. com.trixi.beadwatch.plist --------------------------------------------
echo "$NEW_LABEL:"
label_now="$(plist_label "$NEW_PLIST")"
if [ -f "$NEW_PLIST" ] && [ -n "$label_now" ] && [ "$label_now" != "$NEW_LABEL" ]; then
  todo "$NEW_PLIST has Label '$label_now', not $NEW_LABEL — refusing to touch a file this script does not own"
  new_written=0
else
  current=""
  [ -f "$NEW_PLIST" ] && current="$(cat "$NEW_PLIST")"
  if [ "$current" = "$desired" ]; then
    ok "$NEW_PLIST current (${#REPOS[@]} repo(s) watched)"
    new_written=0
  elif [ "$CHECK" -eq 1 ]; then
    todo "$NEW_PLIST missing or stale — WatchPaths set would change"
    new_written=0
  else
    mkdir -p "$LAUNCH_AGENTS_DIR" "$(dirname "$LOG_PATH")"
    printf '%s' "$desired" > "$NEW_PLIST"
    ok "$NEW_PLIST written (${#REPOS[@]} repo(s) watched)"
    new_written=1
  fi
fi

if [ "$CHECK" -eq 0 ] && [ "${new_written:-0}" -eq 1 ]; then
  launchctl bootout "gui/$UID_NUM/$NEW_LABEL" >/dev/null 2>&1 || true
  launchctl bootstrap "gui/$UID_NUM" "$NEW_PLIST"
  ok "$NEW_LABEL loaded"
fi

# --- 2. retire com.trixi.bd-counts.plist -------------------------------------
echo "$OLD_LABEL (retiring):"
if [ ! -f "$OLD_PLIST" ]; then
  ok "$OLD_LABEL already retired"
else
  label_old="$(plist_label "$OLD_PLIST")"
  if [ -n "$label_old" ] && [ "$label_old" != "$OLD_LABEL" ]; then
    todo "$OLD_PLIST has Label '$label_old', not $OLD_LABEL — refusing to remove a file this script does not own"
  elif [ "$CHECK" -eq 1 ]; then
    todo "$OLD_PLIST still present — run without --check to retire it"
  else
    launchctl bootout "gui/$UID_NUM/$OLD_LABEL" >/dev/null 2>&1 || true
    rm -f "$OLD_PLIST"
    ok "$OLD_PLIST unloaded and removed"
  fi
fi

echo
if [ "$missing" -gt 0 ]; then
  printf '%d item(s) to do — run: bash scripts/tool-install-launchd.sh\n' "$missing"
  exit 1
fi
printf '%s installed; %s retired.\n' "$NEW_LABEL" "$OLD_LABEL"
exit 0
