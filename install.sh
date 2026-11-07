#!/bin/bash
# Build and install this user's Cuevia app and login LaunchAgent.
set -euo pipefail
umask 077

if [[ ${1:-} == --help || ${1:-} == -h ]]; then
  printf '%s\n' 'Usage: ./install.sh' \
    'Builds Cuevia, installs ~/Applications/Cuevia.app, and starts it at GUI login.' \
    'Copies project .env to ~/Library/Application Support/Cuevia/.env when present.' \
    'To remove the installation later, run ./uninstall.sh (settings and recordings are kept).' \
    'Requires macOS 14+, Go 1.27+, and Xcode Command Line Tools. Do not use sudo.'
  exit 0
fi
if [[ $# != 0 ]]; then
  printf 'Unknown argument: %s (see --help)\n' "$1" >&2
  exit 1
fi
fail() { printf 'Cuevia installation failed: %s\n' "$*" >&2; exit 1; }
[[ $(uname -s) == Darwin ]] || fail 'macOS is required.'
[[ $(id -u) != 0 ]] || fail 'run ./install.sh as your login user, without sudo.'
[[ ${HOME:-} == /* ]] || fail 'HOME must be an absolute path.'
macos_version=$(/usr/bin/sw_vers -productVersion)
[[ ${macos_version%%.*} -ge 14 ]] || fail 'macOS 14 or newer is required.'

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
app_path="$HOME/Applications/Cuevia.app"
built_app="$project_dir/dist/Cuevia.app"
support_dir="$HOME/Library/Application Support/Cuevia"
config_path="$support_dir/.env"
agents_dir="$HOME/Library/LaunchAgents"
agent_path="$agents_dir/dev.cuevia.desktop.startup.plist"
label=dev.cuevia.desktop.startup
domain="gui/$(id -u)"
service="$domain/$label"
previous_launch_app=
staging_dir=
config_temp=
config_backup=
agent_temp=
agent_backup=
app_changed=0
had_app=0
config_changed=0
agent_changed=0
job_was_loaded=0
job_unloaded=0
job_registered=0
was_disabled=0
install_started=0
web_changed=0
previous_web_link=

plist_value() { /usr/bin/plutil -extract "$1" raw -o - "$2" 2>/dev/null; }
owned_bundle() {
  [[ -d "$1" && ! -L "$1" ]] &&
    [[ $(plist_value CFBundleIdentifier "$1/Contents/Info.plist") == dev.cuevia.desktop ]] &&
    [[ $(plist_value CFBundleExecutable "$1/Contents/Info.plist") == Cuevia ]]
}

# Match the executable path, never a process name or the owner of an HTTP port.
stop_app() {
  local scope=${1:-all} pid executable attempt remaining=0
  while read -r pid executable; do
    if [[ "$scope" == source ]]; then
      [[ "$executable" == "$built_app/Contents/MacOS/Cuevia" ]] || continue
    else
      case "$executable" in
        "$app_path/Contents/MacOS/Cuevia"|"$built_app/Contents/MacOS/Cuevia") ;;
        *) [[ -n "$previous_launch_app" && "$executable" == "$previous_launch_app/Contents/MacOS/Cuevia" ]] || continue ;;
      esac
    fi
    kill -TERM "$pid" 2>/dev/null || continue
    for ((attempt=0; attempt<50; attempt++)); do
      [[ $(/bin/ps -p "$pid" -o comm= 2>/dev/null) == "$executable" ]] || break
      /bin/sleep 0.1
    done
    if [[ $(/bin/ps -p "$pid" -o comm= 2>/dev/null) == "$executable" ]]; then
      printf 'Cuevia process %s did not quit; no forced termination was used.\n' "$pid" >&2
      remaining=1
    fi
  done < <(/bin/ps -ax -o pid= -o comm=)
  return "$remaining"
}

app_pid() {
  local pid executable
  while read -r pid executable; do
    if [[ "$executable" == "$app_path/Contents/MacOS/Cuevia" ]]; then printf '%s' "$pid"; return 0; fi
  done < <(/bin/ps -ax -o pid= -o comm=)
  return 1
}

cleanup() {
  local status=$? rollback_failed=0
  trap - EXIT HUP INT TERM
  set +e
  if [[ $status != 0 && $install_started == 1 ]]; then
    printf '%s\n' 'Restoring the previous Cuevia installation…' >&2
    if [[ $web_changed == 1 ]]; then
      rm -f "$support_dir/web" || rollback_failed=1
      if [[ -n "$previous_web_link" ]]; then
        ln -s "$previous_web_link" "$support_dir/web" || rollback_failed=1
      fi
    fi
    if [[ $job_registered == 1 ]]; then
      /bin/launchctl bootout "$service" >/dev/null 2>&1
      if /bin/launchctl print "$service" >/dev/null 2>&1; then rollback_failed=1; fi
    fi
    if [[ $app_changed == 1 && ( $had_app == 0 || -e "$staging_dir/previous.app" ) ]]; then
      if stop_app; then
        if ! rm -rf "$app_path"; then
          rollback_failed=1
        elif [[ -e "$staging_dir/previous.app" ]] && ! mv "$staging_dir/previous.app" "$app_path"; then
          rollback_failed=1
        fi
      else
        rollback_failed=1
      fi
    fi
    if [[ $config_changed == 1 ]]; then
      if [[ -n "$config_backup" ]]; then
        mv -f "$config_backup" "$config_path" || rollback_failed=1
      else
        rm -f "$config_path" || rollback_failed=1
      fi
    fi
    if [[ $agent_changed == 1 ]]; then
      if [[ -n "$agent_backup" ]]; then
        mv -f "$agent_backup" "$agent_path" || rollback_failed=1
      else
        rm -f "$agent_path" || rollback_failed=1
      fi
    fi
    if [[ $job_was_loaded == 1 && $job_unloaded == 1 ]]; then
      /bin/launchctl enable "$service" >/dev/null 2>&1 || rollback_failed=1
      if ! /bin/launchctl bootstrap "$domain" "$agent_path"; then
        printf 'Could not restore the login job. Retry: launchctl bootstrap %q %q\n' "$domain" "$agent_path" >&2
        rollback_failed=1
      fi
    fi
    if [[ $was_disabled == 1 ]]; then /bin/launchctl disable "$service" >/dev/null 2>&1 || rollback_failed=1; fi
  fi
  if [[ $rollback_failed == 1 ]]; then
    printf 'Rollback was incomplete; private recovery files are retained in:\n%s\n' "$staging_dir" >&2
    [[ -z "$config_backup" || ! -e "$config_backup" ]] || printf '%s\n' "$config_backup" >&2
    [[ -z "$agent_backup" || ! -e "$agent_backup" ]] || printf '%s\n' "$agent_backup" >&2
    exit "$status"
  fi
  [[ -z "$config_temp" ]] || rm -f "$config_temp"
  [[ -z "$config_backup" ]] || rm -f "$config_backup"
  [[ -z "$agent_temp" ]] || rm -f "$agent_temp"
  [[ -z "$agent_backup" ]] || rm -f "$agent_backup"
  [[ -z "$staging_dir" ]] || rm -rf "$staging_dir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

# Refuse unrelated bundles or login jobs before changing a running installation.
/bin/launchctl print "$domain" >/dev/null 2>&1 || fail 'log in to the macOS desktop before installing.'
if [[ -e "$app_path" || -L "$app_path" ]]; then
  owned_bundle "$app_path" || fail "$app_path is not an owned Cuevia app bundle."
  had_app=1
fi
if [[ -e "$agent_path" || -L "$agent_path" ]]; then
  [[ -f "$agent_path" && ! -L "$agent_path" ]] || fail 'the existing LaunchAgent is not a regular file.'
  [[ $(plist_value Label "$agent_path") == "$label" ]] || fail 'the existing LaunchAgent has a different label.'
  [[ $(plist_value ProgramArguments.0 "$agent_path") == /usr/bin/open &&
     $(plist_value ProgramArguments.1 "$agent_path") == -g &&
     $(plist_value ProgramArguments.2 "$agent_path") == -W ]] || fail 'the existing LaunchAgent has an unrecognized command.'
  if plist_value ProgramArguments.4 "$agent_path" >/dev/null; then fail 'the existing LaunchAgent has unexpected arguments.'; fi
  if plist_value Program "$agent_path" >/dev/null; then fail 'the existing LaunchAgent overrides its executable.'; fi
  if /usr/bin/plutil -extract EnvironmentVariables xml1 -o /dev/null "$agent_path" 2>/dev/null; then
    fail 'the existing LaunchAgent contains environment settings; review it before replacing it.'
  fi
  previous_launch_app=$(plist_value ProgramArguments.3 "$agent_path")
  [[ "$previous_launch_app" == /* && "$previous_launch_app" == */Cuevia.app ]] || fail 'the existing LaunchAgent has an unrecognized app path.'
  if [[ "$previous_launch_app" != "$app_path" ]]; then
    owned_bundle "$previous_launch_app" || fail 'the existing LaunchAgent points to an unrecognized Cuevia bundle.'
  fi
fi
if /bin/launchctl print "$service" >/dev/null 2>&1; then
  [[ -f "$agent_path" ]] || fail 'this login-job label is already loaded without a matching owned plist.'
  # launchctl identifies the plist from which the loaded job was bootstrapped.
  loaded_agent=$(/bin/launchctl print "$service" | /usr/bin/awk '/^[[:space:]]*path = / && !found { sub(/^[[:space:]]*path = /, ""); print; found=1 }')
  [[ "$loaded_agent" == "$agent_path" ]] || fail 'this login-job label is already loaded from another plist.'
  job_was_loaded=1
fi
if /bin/launchctl print-disabled "$domain" | /usr/bin/grep -F '"dev.cuevia.desktop.startup" => true' >/dev/null; then was_disabled=1; fi
if [[ -e "$config_path" || -L "$config_path" ]]; then
  [[ -f "$config_path" && ! -L "$config_path" ]] || fail 'the managed .env must be a regular file.'
fi

# The build replaces dist/Cuevia.app. Stop only that owned development bundle first.
if [[ -e "$built_app" || -L "$built_app" ]]; then
  owned_bundle "$built_app" || fail 'dist/Cuevia.app is not an owned Cuevia app bundle.'
  stop_app source || fail 'quit the development Cuevia app and run ./install.sh again.'
fi
printf '%s\n' 'Building Cuevia before updating the installed app…'
/bin/sh "$project_dir/scripts/build-macos.sh"
/usr/bin/codesign --verify --strict "$built_app"
mkdir -p "$HOME/Applications" "$agents_dir" "$support_dir"
staging_dir=$(mktemp -d "$HOME/Applications/.cuevia-install.XXXXXX")
/usr/bin/ditto "$built_app" "$staging_dir/Cuevia.app"
/usr/bin/codesign --verify --strict "$staging_dir/Cuevia.app"

# Copy bytes only: dotenv files are never sourced, evaluated, parsed, or printed.
config_source=
if [[ -f "$config_path" ]] && /usr/bin/grep -Eq '^[[:space:]]*CUEVIA_SETTINGS_VERSION[[:space:]]*=[[:space:]]*"?1"?[[:space:]]*$' "$config_path"; then
  printf '%s\n' 'Preserving OpenAI settings saved from the Cuevia settings window.'
elif [[ -f "$project_dir/.env" ]]; then
  config_source="$project_dir/.env"
elif [[ ! -f "$config_path" ]]; then
  config_source="$project_dir/.env.example"
fi
if [[ -n "$config_source" ]]; then
  [[ -r "$config_source" ]] || fail 'the source .env (or .env.example) is not readable.'
  config_temp=$(mktemp "$support_dir/.env.install.XXXXXX")
  cp "$config_source" "$config_temp"
  chmod 600 "$config_temp"
  if [[ -f "$config_path" ]]; then
    config_backup=$(mktemp "$support_dir/.env.previous.XXXXXX")
    cp "$config_path" "$config_backup"
    chmod 600 "$config_backup"
  fi
fi

agent_temp=$(mktemp "$agents_dir/.cuevia-startup.XXXXXX")
/usr/bin/plutil -create xml1 "$agent_temp"
/usr/bin/plutil -insert Label -string "$label" "$agent_temp"
/usr/bin/plutil -insert ProgramArguments -json '["/usr/bin/open","-g","-W"]' "$agent_temp"
/usr/bin/plutil -insert ProgramArguments.3 -string "$app_path" "$agent_temp"
/usr/bin/plutil -insert RunAtLoad -bool YES "$agent_temp"
/usr/bin/plutil -lint "$agent_temp" >/dev/null
chmod 644 "$agent_temp"
if [[ -f "$agent_path" ]]; then
  agent_backup=$(mktemp "$agents_dir/.cuevia-startup.previous.XXXXXX")
  cp -p "$agent_path" "$agent_backup"
fi

install_started=1
if [[ $job_was_loaded == 1 ]]; then
  /bin/launchctl bootout "$service"
  job_unloaded=1
fi
stop_app || fail 'quit the existing Cuevia app and run ./install.sh again.'
app_changed=1
if [[ -e "$app_path" ]]; then mv "$app_path" "$staging_dir/previous.app"; fi
mv "$staging_dir/Cuevia.app" "$app_path"
if [[ -n "$config_temp" ]]; then
  config_changed=1
  mv -f "$config_temp" "$config_path"
  config_temp=
fi
chmod 600 "$config_path"
if [[ -L "$support_dir/web" ]]; then previous_web_link=$(readlink "$support_dir/web"); fi
python3 "$project_dir/scripts/update-web.py" --support-dir "$support_dir"
web_changed=1
agent_changed=1
mv -f "$agent_temp" "$agent_path"
agent_temp=
/bin/launchctl enable "$service"
job_registered=1
/bin/launchctl bootstrap "$domain" "$agent_path"
/bin/launchctl kickstart "$service"

# LaunchServices accepting an open request does not establish app startup success.
started_pid=
for ((attempt=0; attempt<100; attempt++)); do
  if started_pid=$(app_pid); then break; fi
  /bin/sleep 0.1
done
[[ -n "$started_pid" ]] || fail 'the installed app did not start; check its configuration and whether its address is already in use.'
/bin/sleep 1
[[ $(/bin/ps -p "$started_pid" -o comm= 2>/dev/null) == "$app_path/Contents/MacOS/Cuevia" ]] || fail 'the installed app exited during startup; check its configuration and server address.'

printf '\nInstalled: %s\nConfig: %s\n' "$app_path" "$config_path"
printf '%s\n' 'Cuevia is running in the menu bar and will start at your next GUI login.' \
  'Menu bar → Quit stops it until you open the app again or log in again.'
if [[ "$config_source" == "$project_dir/.env.example" ]]; then
  printf '%s\n' 'Add OPENAI_API_KEY to the installed config before using voice transcription.'
fi
