#!/bin/bash
# Remove this user's Cuevia installation; preserve all settings and recordings.
set -euo pipefail
umask 077

fail() { printf 'Cuevia removal failed: %s\n' "$*" >&2; exit 1; }
usage() {
  printf '%s\n' \
    'Usage: ./uninstall.sh [--dry-run] [--app /Applications/Cuevia.app]' \
    'Stops Cuevia, unregisters its matching login job, and moves the app to Trash.' \
    'Default app: ~/Applications/Cuevia.app (installed by install.sh).' \
    'Use --app /Applications/Cuevia.app for a DMG installation.' \
    'Preserves API keys, settings, web updates, captures, recordings, and source builds.' \
    'Do not use sudo. --dry-run changes nothing.'
}

dry_run=0
requested_app=
while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --dry-run) dry_run=1; shift ;;
    --app)
      [[ $# -ge 2 && -z "$requested_app" ]] || fail '--app requires one app path.'
      requested_app=$2; shift 2 ;;
    *) fail "unknown argument: $1 (see --help)." ;;
  esac
done

[[ $(uname -s) == Darwin ]] || fail 'macOS is required.'
user_id=$(id -u)
[[ "$user_id" != 0 ]] || fail 'run ./uninstall.sh as your login user, without sudo.'
[[ ${HOME:-} == /* && "$HOME" != / && -d "$HOME" ]] || fail 'HOME must be an absolute home directory.'
app_path="${requested_app:-$HOME/Applications/Cuevia.app}"
case "$app_path" in
  "$HOME/Applications/Cuevia.app"|/Applications/Cuevia.app) ;;
  *) fail '--app must be /Applications/Cuevia.app or your ~/Applications/Cuevia.app.' ;;
esac
agent_path="$HOME/Library/LaunchAgents/dev.cuevia.desktop.startup.plist"
service="gui/$user_id/dev.cuevia.desktop.startup"
app_present=0
agent_present=0
job_loaded=0
trash_dir=

plist_value() { /usr/bin/plutil -extract "$1" raw -o - "$2" 2>/dev/null; }
regular_parent() {
  local directory=$1
  while [[ "$directory" != / ]]; do
    [[ ! -L "$directory" ]] || fail "a parent directory is a symlink: $directory"
    directory=${directory%/*}
    [[ -n "$directory" ]] || directory=/
  done
}

# Validate everything before unloading a job, signalling a process, or moving files.
regular_parent "${app_path%/*}"
regular_parent "${agent_path%/*}"
if [[ -e "$app_path" || -L "$app_path" ]]; then
  [[ -d "$app_path" && ! -L "$app_path" && -O "$app_path" ]] || fail 'the app must be a bundle owned by this user, not a symlink.'
  [[ $(plist_value CFBundleIdentifier "$app_path/Contents/Info.plist") == dev.cuevia.desktop &&
     $(plist_value CFBundleExecutable "$app_path/Contents/Info.plist") == Cuevia ]] || fail 'the selected app is not a recognized Cuevia bundle.'
  [[ -w "${app_path%/*}" ]] || fail 'the app folder is not writable; remove this app with Finder instead.'
  app_present=1
fi
if [[ -e "$agent_path" || -L "$agent_path" ]]; then
  [[ -f "$agent_path" && ! -L "$agent_path" && -O "$agent_path" ]] || fail 'the LaunchAgent must be a regular file owned by this user.'
  [[ $(plist_value Label "$agent_path") == dev.cuevia.desktop.startup &&
     $(plist_value ProgramArguments.0 "$agent_path") == /usr/bin/open &&
     $(plist_value ProgramArguments.1 "$agent_path") == -g &&
     $(plist_value ProgramArguments.2 "$agent_path") == -W ]] || fail 'the existing LaunchAgent has an unrecognized command.'
  if plist_value ProgramArguments.4 "$agent_path" >/dev/null || plist_value Program "$agent_path" >/dev/null; then
    fail 'the existing LaunchAgent overrides its command or has unexpected arguments.'
  fi
  if /usr/bin/plutil -extract EnvironmentVariables xml1 -o /dev/null "$agent_path" 2>/dev/null; then
    fail 'the existing LaunchAgent contains unexpected environment settings.'
  fi
  if [[ $(plist_value ProgramArguments.3 "$agent_path") == "$app_path" ]]; then
    [[ -w "${agent_path%/*}" ]] || fail 'the LaunchAgents folder is not writable.'
    agent_present=1
  else
    printf '%s\n' 'Preserving the login job: it points to a different app installation.'
  fi
fi
if /bin/launchctl print "$service" >/dev/null 2>&1; then
  loaded_agent=$(/bin/launchctl print "$service" | /usr/bin/awk '/^[[:space:]]*path = / && !found { sub(/^[[:space:]]*path = /, ""); print; found=1 }')
  [[ -f "$agent_path" && "$loaded_agent" == "$agent_path" ]] || fail 'the loaded login job has no matching owned plist.'
  if [[ $agent_present == 1 ]]; then job_loaded=1; fi
fi

process_matches() {
  local current_uid current_executable
  read -r current_uid current_executable < <(/bin/ps -p "$1" -o uid= -o comm= 2>/dev/null) || return 1
  [[ "$current_uid" == "$user_id" && "$current_executable" == "$app_path/Contents/MacOS/Cuevia" ]]
}
stop_app() {
  local owner pid executable attempt
  while read -r owner pid executable; do
    [[ "$owner" == "$user_id" && "$executable" == "$app_path/Contents/MacOS/Cuevia" ]] || continue
    process_matches "$pid" || continue
    if [[ $dry_run == 1 ]]; then
      printf 'Would stop Cuevia process: %s\n' "$pid"
      continue
    fi
    if ! kill -TERM "$pid" 2>/dev/null; then
      process_matches "$pid" && fail "could not stop Cuevia process $pid."
      continue
    fi
    for ((attempt=0; attempt<50; attempt++)); do
      if ! process_matches "$pid"; then break; fi
      /bin/sleep 0.1
    done
    if process_matches "$pid"; then
      fail "Cuevia process $pid did not quit; quit it normally and retry. No forced termination was used."
    fi
  done < <(/bin/ps -ax -o uid= -o pid= -o comm=)
}

if [[ $dry_run == 0 && ( $app_present == 1 || $agent_present == 1 ) ]]; then
  regular_parent "$HOME/.Trash"
  [[ ! -e "$HOME/.Trash" || ( -d "$HOME/.Trash" && -O "$HOME/.Trash" ) ]] || fail 'the user Trash is not an owned directory.'
  mkdir -p "$HOME/.Trash"
  trash_dir=$(mktemp -d "$HOME/.Trash/Cuevia-uninstall.XXXXXX")
  printf 'Recovery folder: %s\n' "$trash_dir"
fi
if [[ $job_loaded == 1 ]]; then
  if [[ $dry_run == 1 ]]; then
    printf 'Would unregister login job: %s\n' "$service"
  else
    /bin/launchctl bootout "$service" || fail 'could not unregister the login job; no files were moved.'
    if /bin/launchctl print "$service" >/dev/null 2>&1; then
      fail 'the login job is still loaded; no files were moved.'
    fi
  fi
fi
stop_app
for target in "$agent_path" "$app_path"; do
  if [[ "$target" == "$agent_path" && $agent_present == 0 ]] || [[ "$target" == "$app_path" && $app_present == 0 ]]; then continue; fi
  if [[ $dry_run == 1 ]]; then
    printf 'Would move to Trash: %s\n' "$target"
  else
    mv "$target" "$trash_dir/" || fail "could not move $target; check the recovery folder above."
  fi
done
if [[ $dry_run == 1 ]]; then
  printf '%s\n' 'Dry run complete. Nothing was changed.'
elif [[ $app_present == 0 && $agent_present == 0 ]]; then
  printf '%s\n' 'No matching Cuevia app or login job was installed.'
else
  printf '%s\n' 'Cuevia was removed. The app and login plist can be recovered from Trash.'
fi
printf '%s\n' 'API keys, settings, captures, recordings, archives, and source builds were preserved.'
