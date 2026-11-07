#!/bin/sh
# Install the real offline CPU transcriber in Cuevia's user cache, never the repo.
set -eu
umask 077

case "$(uname -s)" in
  Darwin) cache_root="$HOME/Library/Caches" ;;
  Linux) cache_root="${XDG_CACHE_HOME:-$HOME/.cache}" ;;
  *) printf '%s\n' 'This setup script supports macOS and Linux.' >&2; exit 1 ;;
esac
install_dir="$cache_root/Cuevia/prototype/transcription"
mkdir -p "$install_dir/bin" "$install_dir/models" "$install_dir/tools"
work_dir="$(mktemp -d "$install_dir/.setup.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

for dependency in curl tar c++; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'Missing %s. Install your C++ build tools first (macOS: xcode-select --install).\n' "$dependency" >&2
    exit 1
  fi
done

# Homebrew is optional; a missing CMake is installed only in this cache.
cmake_bin="$(command -v cmake || true)"
if [ -z "$cmake_bin" ]; then
  cmake_bin="$install_dir/tools/cmake-python/cmake/data/bin/cmake"
  if [ ! -x "$cmake_bin" ]; then
    if ! command -v python3 >/dev/null 2>&1; then
      printf '%s\n' 'Install CMake or Python 3 with pip, then run this script again.' >&2
      exit 1
    fi
    python3 -m pip install --disable-pip-version-check --no-deps --target "$install_dir/tools/cmake-python" 'cmake==4.4.4'
  fi
fi

verify_sha256() {
  if command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$1" | cut -d ' ' -f 1)"
  else
    actual="$(sha256sum "$1" | cut -d ' ' -f 1)"
  fi
  [ "$actual" = "$2" ]
}
verify_model() {
  [ -f "$1" ] || return 1
  if command -v shasum >/dev/null 2>&1; then
    actual="$(shasum "$1" | cut -d ' ' -f 1)"
  else
    actual="$(sha1sum "$1" | cut -d ' ' -f 1)"
  fi
  # The model checksum is published by ggerganov/whisper.cpp on Hugging Face.
  [ "$actual" = '465707469ff3a37a2b9b8d8f89f2f99de7299dac' ]
}

if [ ! -x "$install_dir/bin/whisper-cli" ]; then
  printf '%s\n' 'Building official whisper.cpp v1.8.3 for local CPU inference…'
  curl --fail --location --retry 2 --connect-timeout 20 --max-time 300 \
    'https://github.com/ggml-org/whisper.cpp/archive/refs/tags/v1.8.3.tar.gz' \
    --output "$work_dir/whisper.tar.gz"
  if ! verify_sha256 "$work_dir/whisper.tar.gz" '870ba21409cdf66697dc4db15ebdb13bc67037d76c7cc63756c81471d8f1731a'; then
    printf '%s\n' 'whisper.cpp source checksum mismatch. Installation stopped.' >&2
    exit 1
  fi
  tar -xzf "$work_dir/whisper.tar.gz" -C "$work_dir"
  "$cmake_bin" -S "$work_dir/whisper.cpp-1.8.3" -B "$work_dir/build" \
    -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
    -DGGML_METAL=OFF -DGGML_CUDA=OFF -DWHISPER_BUILD_TESTS=OFF -DWHISPER_CURL=OFF
  "$cmake_bin" --build "$work_dir/build" --target whisper-cli --parallel 2
  cp "$work_dir/build/bin/whisper-cli" "$install_dir/bin/.whisper-cli.pending"
  chmod 700 "$install_dir/bin/.whisper-cli.pending"
  mv "$install_dir/bin/.whisper-cli.pending" "$install_dir/bin/whisper-cli"
fi

if ! verify_model "$install_dir/models/ggml-base.bin"; then
  printf '%s\n' 'Downloading multilingual Whisper base model (142 MiB; supports Korean)…'
  curl --fail --location --retry 2 --connect-timeout 20 --max-time 600 \
    'https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin' \
    --output "$work_dir/ggml-base.bin"
  if ! verify_model "$work_dir/ggml-base.bin"; then
    printf '%s\n' 'Whisper model checksum mismatch. Installation stopped.' >&2
    exit 1
  fi
  mv "$work_dir/ggml-base.bin" "$install_dir/models/ggml-base.bin"
fi

"$install_dir/bin/whisper-cli" --help >/dev/null 2>&1
printf '\n%s\n%s\n' 'Offline Korean transcription is ready. Run go run .' \
  'Audio stays on this computer. No API key or transcription service is used.'
