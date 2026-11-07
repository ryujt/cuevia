#!/usr/bin/env python3
"""Install Cuevia web assets atomically; the app bundle and sessions stay intact."""
import argparse
import hashlib
import os
from pathlib import Path
import shutil
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--support-dir', type=Path,
                        default=Path.home() / 'Library/Application Support/Cuevia')
    args = parser.parse_args()
    source = Path(__file__).resolve().parents[1] / 'web'
    files = sorted(p for p in source.rglob('*') if p.is_file())
    digest = hashlib.sha256()
    for path in files:
        digest.update(str(path.relative_to(source)).encode())
        digest.update(path.read_bytes())
    support = args.support_dir.expanduser().resolve()
    releases = support / 'web-releases'
    releases.mkdir(parents=True, exist_ok=True, mode=0o700)
    release = releases / digest.hexdigest()[:20]
    if not release.exists():
        with tempfile.TemporaryDirectory(prefix='.web-install-', dir=releases) as temp:
            staged = Path(temp) / 'web'
            shutil.copytree(source, staged)
            for path in staged.rglob('*'):
                path.chmod(0o700 if path.is_dir() else 0o600)
            staged.chmod(0o700)
            os.rename(staged, release)
    with tempfile.TemporaryDirectory(prefix='.web-link-', dir=support) as temp:
        link = Path(temp) / 'web'
        link.symlink_to(release, target_is_directory=True)
        os.replace(link, support / 'web')
    print(f'Updated Cuevia web: {support / "web"}')
    print('Reload the Cuevia browser tab. No app restart or signing is needed.')


if __name__ == '__main__':
    main()
