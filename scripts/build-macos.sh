#!/bin/sh
# Build and sign a native agent app. This script never launches or installs it.
set -eu

if [ "$(uname -s)" != 'Darwin' ]; then
  printf '%s\n' 'Cuevia.app must be built on macOS with Xcode Command Line Tools.' >&2
  exit 1
fi
if ! command -v go >/dev/null 2>&1; then
  printf '%s\n' 'Go 1.27 or newer is required.' >&2
  exit 1
fi
/usr/bin/xcrun --find clang >/dev/null

project_dir="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
app_version="${CUEVIA_APP_VERSION:-0.2.0}"
build_number="${CUEVIA_BUILD_NUMBER:-3}"
if ! printf '%s\n' "$app_version" | /usr/bin/grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' ||
   ! printf '%s\n' "$build_number" | /usr/bin/grep -Eq '^[0-9]+$'; then
  printf '%s\n' 'Use a numeric x.y.z CUEVIA_APP_VERSION and numeric CUEVIA_BUILD_NUMBER.' >&2
  exit 1
fi
architectures="${CUEVIA_ARCHS:-$(GOWORK=off go env GOHOSTARCH)}"
case "$architectures" in
  arm64|amd64|'arm64 amd64'|'amd64 arm64') ;;
  *) printf '%s\n' 'CUEVIA_ARCHS must be arm64, amd64, or "arm64 amd64".' >&2; exit 1 ;;
esac
signing_identity="${CUEVIA_SIGN_IDENTITY:-}"
if [ -z "$signing_identity" ]; then
  # Keep local updates on the existing signer when release credentials have
  # also been added to the keychain. Distribution passes an explicit identity.
  bundle_authority() {
    if [ -d "$1" ] && /usr/bin/codesign --verify --strict "$1" >/dev/null 2>&1; then
      /usr/bin/codesign --display --verbose=4 "$1" 2>&1 |
        /usr/bin/awk '/^Authority=/ { sub(/^Authority=/, ""); print; exit }'
    fi
  }
  installed_authority=''
  if [ -n "${HOME:-}" ]; then
    installed_authority="$(bundle_authority "$HOME/Applications/Cuevia.app")"
  fi
  built_authority="$(bundle_authority "$project_dir/dist/Cuevia.app")"
  signing_identity="$(/usr/bin/security find-identity -v -p codesigning |
    /usr/bin/awk -v installed_authority="$installed_authority" -v built_authority="$built_authority" \
      -f "$project_dir/scripts/select-signing-identity.awk")"
fi
printf 'Using signing identity: %s\n' "$signing_identity"
icon_source="$project_dir/assets/Cuevia.png"
if [ ! -f "$icon_source" ]; then
  printf 'Missing app icon: %s\n' "$icon_source" >&2
  exit 1
fi
output_dir="$project_dir/dist"
app_path="$output_dir/Cuevia.app"
mkdir -p "$output_dir"
staging_dir="$(mktemp -d "$output_dir/.cuevia-build.XXXXXX")"
trap 'rm -rf "$staging_dir"' EXIT HUP INT TERM
bundle_path="$staging_dir/Cuevia.app"
mkdir -p "$bundle_path/Contents/MacOS" "$bundle_path/Contents/Resources"

cat > "$bundle_path/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>ko</string>
  <key>CFBundleDisplayName</key><string>Cuevia</string>
  <key>CFBundleName</key><string>Cuevia</string>
  <key>CFBundleExecutable</key><string>Cuevia</string>
  <key>CFBundleIconFile</key><string>Cuevia.icns</string>
  <key>CFBundleIdentifier</key><string>dev.cuevia.desktop</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>CFBundleVersion</key><string>2</string>
  <key>LSApplicationCategoryType</key><string>public.app-category.productivity</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSPrincipalClass</key><string>NSApplication</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSMicrophoneUsageDescription</key>
  <string>화면에 대한 음성 설명을 녹음하고 OpenAI API로 전사하는 데 사용합니다.</string>
  <key>NSScreenCaptureUsageDescription</key>
  <string>선택한 모니터나 프로그램 화면을 캡처하여 음성과 설명을 함께 저장합니다.</string>
</dict>
</plist>
PLIST
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $app_version" "$bundle_path/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $build_number" "$bundle_path/Contents/Info.plist"
printf 'APPL????' > "$bundle_path/Contents/PkgInfo"

iconset_path="$staging_dir/Cuevia.iconset"
mkdir -p "$iconset_path"
for size in 16 32 128 256 512; do
  /usr/bin/sips -z "$size" "$size" "$icon_source" --out "$iconset_path/icon_${size}x${size}.png" >/dev/null
  retina_size=$((size * 2))
  /usr/bin/sips -z "$retina_size" "$retina_size" "$icon_source" --out "$iconset_path/icon_${size}x${size}@2x.png" >/dev/null
done
/usr/bin/iconutil -c icns "$iconset_path" -o "$bundle_path/Contents/Resources/Cuevia.icns"

cd "$project_dir"
for architecture in $architectures; do
  case "$architecture" in arm64) clang_arch=arm64 ;; amd64) clang_arch=x86_64 ;; esac
  printf 'Building Cuevia.app %s for macOS %s with cgo…\n' "$app_version" "$clang_arch"
  GOWORK=off MACOSX_DEPLOYMENT_TARGET=14.0 \
    CGO_CFLAGS="${CGO_CFLAGS:-} -arch $clang_arch -mmacosx-version-min=14.0" \
    CGO_CXXFLAGS="${CGO_CXXFLAGS:-} -arch $clang_arch -mmacosx-version-min=14.0" \
    CGO_LDFLAGS="${CGO_LDFLAGS:-} -arch $clang_arch -mmacosx-version-min=14.0" \
    CGO_ENABLED=1 GOOS=darwin GOARCH="$architecture" \
    go build -trimpath -o "$staging_dir/Cuevia-$architecture" .
done
case "$architectures" in
  arm64|amd64) cp "$staging_dir/Cuevia-$architectures" "$bundle_path/Contents/MacOS/Cuevia" ;;
  *) /usr/bin/lipo -create "$staging_dir/Cuevia-arm64" "$staging_dir/Cuevia-amd64" -output "$bundle_path/Contents/MacOS/Cuevia" ;;
esac
/usr/bin/lipo -archs "$bundle_path/Contents/MacOS/Cuevia"
chmod 755 "$bundle_path/Contents/MacOS/Cuevia"
/usr/bin/plutil -lint "$bundle_path/Contents/Info.plist"
if [ "$signing_identity" = '-' ]; then
  printf '%s\n' 'Explicit ad-hoc signing: macOS privacy permissions may need registration again after rebuilding.'
  /usr/bin/codesign --force --sign - --identifier dev.cuevia.desktop "$bundle_path"
else
  /usr/bin/codesign --force --options runtime --timestamp --sign "$signing_identity" --identifier dev.cuevia.desktop "$bundle_path"
fi
/usr/bin/codesign --verify --strict "$bundle_path"
/usr/bin/codesign -d -r- "$bundle_path"

if [ -e "$app_path" ]; then
  mv "$app_path" "$staging_dir/previous.app"
fi
if ! mv "$bundle_path" "$app_path"; then
  if [ -e "$staging_dir/previous.app" ]; then
    mv "$staging_dir/previous.app" "$app_path"
  fi
  exit 1
fi
printf '\n%s\n%s\n' 'Built dist/Cuevia.app (dev.cuevia.desktop).' \
  'Launch it with: open dist/Cuevia.app'
