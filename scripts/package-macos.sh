#!/bin/sh
# Build a drag-to-install DMG. Running distribution mode uploads the DMG to Apple.
# Never enable shell tracing here: notarization credentials must remain private.
set +x
set -eu
umask 077

die() {
  printf '%s\n' "$1" >&2
  exit 1
}

if [ "$(uname -s)" != Darwin ]; then
  die 'Cuevia DMGs must be packaged on macOS with Xcode Command Line Tools.'
fi
if [ "$#" -ne 0 ]; then
  die 'Usage: [CUEVIA_RELEASE_MODE=distribution|preview] [CUEVIA_ARCHS="arm64 amd64"] scripts/package-macos.sh'
fi
for required_tool in python3; do
  command -v "$required_tool" >/dev/null 2>&1 || die "$required_tool is required."
done
for required_tool in /usr/bin/security /usr/bin/codesign /usr/bin/ditto /usr/bin/hdiutil /usr/bin/lipo /usr/bin/shasum /usr/bin/xcrun; do
  [ -x "$required_tool" ] || die "Required macOS tool is unavailable: $required_tool"
done

project_dir="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
repository_dir="$(CDPATH= cd -- "$project_dir/../.." && pwd)"
release_mode="${CUEVIA_RELEASE_MODE:-distribution}"
requested_archs="${CUEVIA_ARCHS:-arm64 amd64}"
prebuilt_app="${CUEVIA_PREBUILT_APP:-}"
prebuilt_mode=false
if [ -n "$prebuilt_app" ]; then
  case "$prebuilt_app" in
    /*) ;;
    *) die 'CUEVIA_PREBUILT_APP must be an absolute path to a signed Cuevia.app bundle.' ;;
  esac
  [ -d "$prebuilt_app" ] && [ ! -L "$prebuilt_app" ] || die 'CUEVIA_PREBUILT_APP must be a regular app bundle directory.'
  prebuilt_mode=true
else
  command -v go >/dev/null 2>&1 || die 'Go is required when CUEVIA_PREBUILT_APP is not set.'
fi
case "$release_mode" in
  distribution|preview) ;;
  *) die 'CUEVIA_RELEASE_MODE must be distribution or preview.' ;;
esac
expected_archs="$(CUEVIA_PACKAGE_ARCHS="$requested_archs" python3 - <<'PY'
import os
import sys

requested = os.environ["CUEVIA_PACKAGE_ARCHS"].split()
mapping = {"arm64": "arm64", "amd64": "x86_64"}
if not requested or any(arch not in mapping for arch in requested):
    sys.exit('CUEVIA_ARCHS must contain arm64, amd64, or both, separated by spaces.')
if len(set(requested)) != len(requested):
    sys.exit('CUEVIA_ARCHS must not contain duplicate architectures.')
print(" ".join(sorted(mapping[arch] for arch in requested)))
PY
)"

# Validate credentials before building. Read the API key file only inside notarytool.
notary_credentials=''
if [ "$release_mode" = distribution ]; then
  /usr/bin/xcrun --find notarytool >/dev/null
  /usr/bin/xcrun --find stapler >/dev/null
  if [ -n "${CUEVIA_NOTARY_PROFILE:-}" ]; then
    if [ -n "${CUEVIA_NOTARY_KEY:-}${CUEVIA_NOTARY_KEY_ID:-}${CUEVIA_NOTARY_ISSUER:-}" ]; then
      die 'Use either CUEVIA_NOTARY_PROFILE or the three CUEVIA_NOTARY_KEY credentials, not both.'
    fi
    notary_credentials=profile
  elif [ -n "${CUEVIA_NOTARY_KEY:-}" ] && [ -n "${CUEVIA_NOTARY_KEY_ID:-}" ] && [ -n "${CUEVIA_NOTARY_ISSUER:-}" ]; then
    [ -f "$CUEVIA_NOTARY_KEY" ] && [ -r "$CUEVIA_NOTARY_KEY" ] || die 'CUEVIA_NOTARY_KEY must name a readable App Store Connect API key file.'
    notary_credentials=key
  else
    die 'Distribution requires CUEVIA_NOTARY_PROFILE, or CUEVIA_NOTARY_KEY with CUEVIA_NOTARY_KEY_ID and CUEVIA_NOTARY_ISSUER. Use CUEVIA_RELEASE_MODE=preview only for an unnotarized preview.'
  fi
fi

output_dir="$project_dir/dist"
release_dir="$output_dir/releases"
mkdir -p "$release_dir"
lock_dir="$output_dir/.cuevia-package.lock"
if ! mkdir "$lock_dir" 2>/dev/null; then
  die 'Another packaging run owns dist/.cuevia-package.lock. If a previous run was interrupted, confirm it has stopped before removing that directory.'
fi
staging_dir=''
mounted_image=''
cleanup() {
  cleanup_status=$?
  trap - EXIT HUP INT TERM
  if [ -n "$mounted_image" ]; then
    if ! /usr/bin/hdiutil detach "$mounted_image" >/dev/null 2>&1; then
      printf 'Could not detach the temporary disk image at %s. Its staging directory was retained.\n' "$mounted_image" >&2
      rmdir "$lock_dir" 2>/dev/null || :
      exit 1
    fi
  fi
  if [ -n "$staging_dir" ]; then
    rm -rf "$staging_dir"
  fi
  rmdir "$lock_dir" 2>/dev/null || :
  exit "$cleanup_status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
staging_dir="$(mktemp -d "$output_dir/.cuevia-package.XXXXXX")"
chmod 700 "$staging_dir"

# Resolve only an identity listed as valid by the keychain. An explicit name or
# SHA-1 must resolve to exactly one permitted certificate, never an ad-hoc signer.
if ! /usr/bin/security find-identity -v -p codesigning > "$staging_dir/identities.txt"; then
  if [ "$prebuilt_mode" != true ] || [ -n "${CUEVIA_SIGN_IDENTITY:-}" ]; then
    die 'Could not inspect the signing identities in the local keychain.'
  fi
fi
signing_identity="$(python3 - "$release_mode" "$staging_dir/identities.txt" "$prebuilt_mode" <<'PY'
import os
import re
import sys

mode, identity_file, prebuilt = sys.argv[1:]
requested = os.environ.get("CUEVIA_SIGN_IDENTITY", "")
prefixes = ("Developer ID Application:",) if mode == "distribution" or prebuilt == "true" else ("Apple Development:", "Developer ID Application:")
identities = []
with open(identity_file, encoding="utf-8") as source:
    for line in source:
        match = re.match(r'^\s*\d+\)\s+([0-9A-Fa-f]{40})\s+"([^"]+)"\s*$', line)
        if not match:
            continue
        fingerprint, name = match.groups()
        if name.startswith(prefixes) and (not requested or requested.upper() == fingerprint.upper() or requested == name):
            identities.append(fingerprint.upper())
identities = list(dict.fromkeys(identities))
if prebuilt == "true" and not requested and len(identities) != 1:
    # A cloud-signed app needs no local private key. Leave its DMG unsigned when
    # no unique local Developer ID identity is available.
    print("")
    sys.exit(0)
if len(identities) != 1:
    certificate_type = "Developer ID Application" if mode == "distribution" else "Apple Development or Developer ID Application"
    sys.exit(f'Set CUEVIA_SIGN_IDENTITY to exactly one valid {certificate_type} certificate name or SHA-1 from security find-identity -v -p codesigning.')
print(identities[0])
PY
)"

# The builder receives a public certificate identity, never signing key material.
if [ "$prebuilt_mode" = true ]; then
  app_source="$prebuilt_app"
else
  # Keep the staging parent private while making the public app world-readable.
  (umask 022; CUEVIA_ARCHS="$requested_archs" CUEVIA_SIGN_IDENTITY="$signing_identity" /bin/sh "$project_dir/scripts/build-macos.sh")
  app_source="$output_dir/Cuevia.app"
fi
[ -d "$app_source" ] && [ ! -L "$app_source" ] || die 'The build did not produce a regular dist/Cuevia.app bundle.'
dmg_root="$staging_dir/image-root"
mkdir "$dmg_root"
staged_app="$dmg_root/Cuevia.app"

# Explicit bundle and public-example allowlists prevent source data, private .env
# files, sessions, and key files from entering the image, even after a bad build.
python3 - "$app_source" "$project_dir/.env.example" <<'PY'
from pathlib import Path
import sys

app, example = map(Path, sys.argv[1:])
allowed_directories = {"Contents", "Contents/MacOS", "Contents/Resources", "Contents/_CodeSignature"}
allowed_files = {
    "Contents/Info.plist", "Contents/PkgInfo", "Contents/MacOS/Cuevia",
    "Contents/Resources/Cuevia.icns", "Contents/_CodeSignature/CodeResources",
}
for entry in app.rglob("*"):
    relative = entry.relative_to(app).as_posix()
    if entry.is_symlink() or (entry.is_dir() and relative not in allowed_directories) or (entry.is_file() and relative not in allowed_files) or not (entry.is_file() or entry.is_dir()):
        sys.exit(f'Unexpected app bundle entry; refusing to package: {relative}')
for relative in allowed_files:
    if not (app / relative).is_file():
        sys.exit(f'Required app bundle file is missing: {relative}')
if example.is_symlink() or not example.is_file():
    sys.exit('The public .env.example must be a regular file.')
allowed_values = {
    "OPENAI_API_KEY": {"", "''", '""'},
    "OPENAI_TRANSCRIPTION_MODEL": {"whisper-1", "'whisper-1'", '"whisper-1"'},
    "CUEVIA_ADDR": {"127.0.0.1:8742", "'127.0.0.1:8742'", '"127.0.0.1:8742"'},
}
seen = set()
for line in example.read_text(encoding="utf-8").splitlines():
    line = line.strip()
    if not line or line.startswith("#"):
        continue
    name, separator, value = line.partition("=")
    name, value = name.strip(), value.strip()
    if not separator or name not in allowed_values or value not in allowed_values[name] or name in seen:
        sys.exit('The public .env.example contains an unexpected or non-public setting. No setting values were printed.')
    seen.add(name)
if seen != set(allowed_values):
    sys.exit('The public .env.example is missing required example settings.')
PY
/usr/bin/ditto "$app_source" "$staged_app"

verify_signature() {
  /usr/bin/codesign --verify --strict --verbose=2 '-R=anchor apple generic' "$1"
  /usr/bin/codesign --display --verbose=4 "$1" 2> "$staging_dir/$2.codesign.txt"
  signature_authority="$(/usr/bin/awk '/^Authority=/ { sub(/^Authority=/, ""); print; exit }' "$staging_dir/$2.codesign.txt")"
  case "$signature_authority" in
    'Developer ID Application:'*) verified_signing=developer-id ;;
    'Apple Development:'*) verified_signing=development ;;
    *) die 'The signed artifact does not have an allowed Apple signing certificate.' ;;
  esac
  if { [ "$release_mode" = distribution ] || [ "$prebuilt_mode" = true ]; } && [ "$verified_signing" != developer-id ]; then
    die 'Distribution artifacts must be signed with Developer ID Application.'
  fi
  /usr/bin/codesign --display "--extract-certificates=$staging_dir/$2.certificate-" "$1" >/dev/null 2>&1
  [ -f "$staging_dir/$2.certificate-0" ] || die 'The artifact has no extractable signing certificate.'
  actual_identity="$(/usr/bin/shasum -a 1 "$staging_dir/$2.certificate-0" | /usr/bin/awk '{ print toupper($1) }')"
  if [ -n "${4:-}" ]; then
    [ "$actual_identity" = "$4" ] || die 'The artifact was signed with a different certificate than the expected identity.'
  fi
  if [ "$3" = app ]; then
    /usr/bin/grep -Eq '^CodeDirectory .*flags=.*runtime' "$staging_dir/$2.codesign.txt" || die 'The app is missing the Hardened Runtime signing option.'
    if [ "$release_mode" = distribution ] || [ "$prebuilt_mode" = true ]; then
      /usr/bin/grep -Eq '^Timestamp=' "$staging_dir/$2.codesign.txt" || die 'The distribution app is missing a secure signing timestamp.'
    fi
  fi
}

read_architectures() {
  raw_architectures="$(/usr/bin/lipo -archs "$1")"
  verified_archs="$(printf '%s\n' "$raw_architectures" | /usr/bin/tr ' ' '\n' | LC_ALL=C /usr/bin/sort | /usr/bin/awk 'NF { if (count++) printf " "; printf "%s", $0 } END { print "" }')"
  [ "$verified_archs" = "$expected_archs" ] || die 'The actual lipo architectures do not match CUEVIA_ARCHS.'
}

if [ "$prebuilt_mode" = true ]; then
  verify_signature "$staged_app" app app
else
  verify_signature "$staged_app" app app "$signing_identity"
fi
actual_signing="$verified_signing"
actual_app_identity="$actual_identity"
read_architectures "$staged_app/Contents/MacOS/Cuevia"
actual_archs="$verified_archs"
case "$actual_archs" in
  'arm64 x86_64') architecture_label=universal ;;
  arm64) architecture_label=arm64 ;;
  x86_64) architecture_label=x86_64 ;;
  *) die 'The app contains an unsupported architecture set.' ;;
esac
bundle_metadata="$(python3 - "$staged_app/Contents/Info.plist" <<'PY'
import plistlib
import re
import sys

with open(sys.argv[1], "rb") as source:
    info = plistlib.load(source)
version = info.get("CFBundleShortVersionString")
build = info.get("CFBundleVersion")
minimum = info.get("LSMinimumSystemVersion")
if not isinstance(version, str) or not re.fullmatch(r"[0-9]+(?:\.[0-9]+){0,2}", version):
    sys.exit('The actual app version is not a valid numeric bundle version.')
if not isinstance(build, str) or not re.fullmatch(r"[0-9]+(?:\.[0-9]+){0,2}", build):
    sys.exit('The actual app build is not a valid numeric bundle build.')
if minimum != "14.0" or info.get("CFBundleExecutable") != "Cuevia" or info.get("CFBundleIdentifier") != "dev.cuevia.desktop":
    sys.exit('The app bundle minimum macOS version, executable, or identifier is unexpected.')
print(version, build)
PY
)"
version="${bundle_metadata%% *}"
build="${bundle_metadata#* }"
file_name="Cuevia-${version}-${build}-macOS-${architecture_label}.dmg"
final_dmg="$release_dir/$file_name"
for artifact_path in "$final_dmg" "$final_dmg.sha256" "$final_dmg.release.json"; do
  if [ -e "$artifact_path" ] || [ -L "$artifact_path" ]; then
    die 'A release artifact for this version/build/architecture already exists. Keep published downloads immutable; choose a new CUEVIA_APP_VERSION or CUEVIA_BUILD_NUMBER.'
  fi
done

submit_for_notarization() {
  printf '%s\n' 'Uploading the release artifact to Apple for notarization; waiting for the result.'
  if [ "$notary_credentials" = profile ]; then
    /usr/bin/xcrun notarytool submit "$1" --wait --keychain-profile "$CUEVIA_NOTARY_PROFILE" --output-format json > "$staging_dir/$2.notary-result.json"
  else
    /usr/bin/xcrun notarytool submit "$1" --wait --key "$CUEVIA_NOTARY_KEY" --key-id "$CUEVIA_NOTARY_KEY_ID" --issuer "$CUEVIA_NOTARY_ISSUER" --output-format json > "$staging_dir/$2.notary-result.json"
  fi
  python3 - "$staging_dir/$2.notary-result.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    result = json.load(source)
print('Apple notarization:', result.get('status', 'unknown'), result.get('id', 'no-submission-id'))
if result.get("status") != "Accepted":
    sys.exit('Apple did not accept the notarization submission. No release artifacts or download metadata were published.')
PY
}

if [ "$release_mode" = distribution ]; then
  # Staple the inner app first so its ticket survives dragging it out of the DMG.
  app_archive="$staging_dir/Cuevia-notarization.zip"
  /usr/bin/ditto -c -k --keepParent "$staged_app" "$app_archive"
  submit_for_notarization "$app_archive" app
  /usr/bin/xcrun stapler staple "$staged_app"
  /usr/bin/xcrun stapler validate "$staged_app"
  verify_signature "$staged_app" stapled-app app "$actual_app_identity"
fi

ln -s /Applications "$dmg_root/Applications"
cp "$project_dir/scripts/installer-guide.txt" "$dmg_root/README.txt"
cp "$project_dir/.env.example" "$dmg_root/Cuevia.env.example"
if [ "$release_mode" = preview ]; then
  if [ "$actual_signing" = development ]; then
    preview_description='Apple Development signed · preview not notarized by Apple'
  else
    preview_description='Developer ID Application signed · preview not notarized by Apple'
  fi
  printf '\nRelease status of this image\n%s\nThis is not a notarized release for general distribution. If macOS blocks the app, ask the distributor for a notarized release.\n' "$preview_description" >> "$dmg_root/README.txt"
  printf '%s\n' "$preview_description"
else
  printf '\nRelease status of this image\nDeveloper ID Application signed · release notarized by Apple\n' >> "$dmg_root/README.txt"
fi
chmod 755 "$dmg_root"
chmod 644 "$dmg_root/README.txt" "$dmg_root/Cuevia.env.example"
candidate_dmg="$staging_dir/$file_name"
/usr/bin/hdiutil create -volname Cuevia -srcfolder "$dmg_root" -fs HFS+ -format UDZO "$candidate_dmg"
if [ -n "$signing_identity" ]; then
  if [ "$release_mode" = distribution ]; then
    /usr/bin/codesign --force --sign "$signing_identity" --timestamp "$candidate_dmg"
  else
    /usr/bin/codesign --force --sign "$signing_identity" "$candidate_dmg"
  fi
  verify_signature "$candidate_dmg" dmg dmg "$signing_identity"
else
  printf '%s\n' 'The inner app is Developer ID signed; the DMG container is unsigned because no unique local Developer ID signing identity is available.'
fi

notarized=false
if [ "$release_mode" = distribution ]; then
  submit_for_notarization "$candidate_dmg" dmg
  /usr/bin/xcrun stapler staple "$candidate_dmg"
  /usr/bin/xcrun stapler validate "$candidate_dmg"
  if [ -n "$signing_identity" ]; then
    verify_signature "$candidate_dmg" notarized-dmg dmg "$signing_identity"
  fi
  notarized=true
fi

/usr/bin/hdiutil verify "$candidate_dmg"
mount_dir="$staging_dir/mounted-image"
mkdir "$mount_dir"
# Mark the intended mount before attaching so an interruption still detaches it.
mounted_image="$mount_dir"
if ! /usr/bin/hdiutil attach -readonly -nobrowse -mountpoint "$mount_dir" -plist "$candidate_dmg" > "$staging_dir/attached-image.plist"; then
  mounted_image=''
  die 'Could not attach the completed DMG for verification.'
fi
python3 - "$mount_dir" "$staging_dir/attached-image.plist" "$dmg_root" <<'PY'
from pathlib import Path
import plistlib
import sys
import unicodedata

mount, attached_plist, source = map(Path, sys.argv[1:])
with attached_plist.open("rb") as stream:
    attached = plistlib.load(stream)
mount_points = [entity.get("mount-point") for entity in attached.get("system-entities", [])]
if str(mount) not in mount_points:
    sys.exit('hdiutil did not report the expected private mount point.')
expected = {"Cuevia.app", "Applications", "README.txt", "Cuevia.env.example"}
# HFS+ stores non-ASCII filenames in decomposed Unicode. Compare canonical names
# without accepting any extra entries or canonically equivalent duplicates.
entries = list(mount.iterdir())
actual = {unicodedata.normalize("NFC", entry.name) for entry in entries}
if actual != expected or len(entries) != len(expected):
    sys.exit('The DMG root contains missing or unexpected files; refusing to publish.')
if not (mount / "Applications").is_symlink() or (mount / "Applications").readlink() != Path("/Applications"):
    sys.exit('The DMG Applications link is invalid.')
if (mount / "Cuevia.app").is_symlink() or not (mount / "Cuevia.app").is_dir():
    sys.exit('The DMG app is not a regular app bundle.')
for name in ("README.txt", "Cuevia.env.example"):
    target = mount / name
    if target.is_symlink() or not target.is_file() or target.read_bytes() != (source / name).read_bytes():
        sys.exit('A public DMG document does not match its validated source.')
print('Verified DMG root: Applications -> /Applications, Cuevia.app, Cuevia.env.example, README.txt')
PY
verify_signature "$mount_dir/Cuevia.app" mounted-app app "$actual_app_identity"
[ "$verified_signing" = "$actual_signing" ] || die 'The app in the DMG has a different signing certificate type.'
read_architectures "$mount_dir/Cuevia.app/Contents/MacOS/Cuevia"
cmp "$staged_app/Contents/Info.plist" "$mount_dir/Cuevia.app/Contents/Info.plist"
cmp "$staged_app/Contents/MacOS/Cuevia" "$mount_dir/Cuevia.app/Contents/MacOS/Cuevia"
/usr/bin/hdiutil detach "$mount_dir" >/dev/null
mounted_image=''

# Prepare every output before publishing. Each rename is atomic; the shared site
# manifest is replaced last. Existing versioned releases are never overwritten.
python3 - "$candidate_dmg" "$final_dmg" "$repository_dir/src/data/cuevia-release.json" "$staged_app/Contents/Info.plist" "$actual_archs" "$actual_signing" "$notarized" <<'PY'
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import plistlib
import sys
import tempfile

candidate, final, manifest, info_file = map(Path, sys.argv[1:5])
architectures = sys.argv[5].split()
signing = sys.argv[6]
notarized = sys.argv[7] == "true"
with info_file.open("rb") as stream:
    info = plistlib.load(stream)
digest = hashlib.sha256()
with candidate.open("rb") as stream:
    for block in iter(lambda: stream.read(1024 * 1024), b""):
        digest.update(block)
metadata = {
    "version": info["CFBundleShortVersionString"],
    "build": info["CFBundleVersion"],
    "date": datetime.now().astimezone().date().isoformat(),
    "minMacOS": info["LSMinimumSystemVersion"],
    "architectures": architectures,
    "downloadUrl": "/downloads/cuevia/" + final.name,
    "fileName": final.name,
    "sizeBytes": candidate.stat().st_size,
    "sha256": digest.hexdigest(),
    "signing": signing,
    "notarized": notarized,
}
release_json = Path(str(final) + ".release.json")
checksum = Path(str(final) + ".sha256")
destinations = (final, checksum, release_json)
if any(os.path.lexists(path) for path in destinations):
    sys.exit('A versioned release appeared during packaging; existing artifacts were preserved.')
if manifest.is_symlink():
    sys.exit('Refusing to replace a symlink at the release manifest path.')
manifest.parent.mkdir(parents=True, exist_ok=True)
json_bytes = (json.dumps(metadata, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
temporary_files = []
published = []

def prepare(destination, content):
    descriptor, name = tempfile.mkstemp(prefix=".cuevia-release-", dir=destination.parent)
    temporary = Path(name)
    temporary_files.append(temporary)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(content)
        stream.flush()
        os.fsync(stream.fileno())
    temporary.chmod(0o644)
    return temporary

try:
    checksum_temp = prepare(checksum, f'{metadata["sha256"]}  {final.name}\n'.encode("utf-8"))
    release_temp = prepare(release_json, json_bytes)
    manifest_temp = prepare(manifest, json_bytes)
    candidate.chmod(0o644)
    os.replace(candidate, final)
    published.append(final)
    os.replace(checksum_temp, checksum)
    published.append(checksum)
    os.replace(release_temp, release_json)
    published.append(release_json)
    os.replace(manifest_temp, manifest)
except BaseException:
    for path in reversed(published):
        path.unlink(missing_ok=True)
    raise
finally:
    for path in temporary_files:
        path.unlink(missing_ok=True)
print(f'Packaged {final.name}: {metadata["sizeBytes"]} bytes; {signing}; notarized={str(notarized).lower()}; architectures={" ".join(architectures)}')
print(f'SHA-256: {metadata["sha256"]}')
print(f'Release metadata: {release_json}')
print(f'Site manifest: {manifest}')
PY
printf '\nCreated %s\n' "$final_dmg"
