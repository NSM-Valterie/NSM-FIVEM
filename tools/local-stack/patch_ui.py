"""Reproducible, guarded telemetry-only patch for the v0.7.6 UI ZIP.
Usage: python patch_ui.py original-ui.zip ui.local-first.zip
Never modifies the input file. No API addresses or ownership checks are changed.
"""
import sys
import zipfile
from pathlib import Path


def patch(source, target):
    if Path(source).resolve() == Path(target).resolve():
        raise ValueError('Input and output must differ')
    old = b'init({dsn:`http://127.0.0.1/sentry-ingest`,tracesSampleRate:.05'
    new = b'init({enabled:!1,dsn:void 0,tracesSampleRate:0'
    with zipfile.ZipFile(source) as archive:
        contents = [(i, archive.read(i)) for i in archive.infolist()]
    count = sum(body.count(old) for _, body in contents)
    if count != 1:
        raise ValueError(f'Expected exactly one known v0.7.6 Sentry initialization; found {count}')
    with zipfile.ZipFile(target, 'w', zipfile.ZIP_DEFLATED) as archive:
        for info, body in contents:
            archive.writestr(info, body.replace(old, new))
    print('Disabled optional Sentry initialization; API endpoints and game files unchanged.')


if __name__ == '__main__':
    patch(*sys.argv[1:])
