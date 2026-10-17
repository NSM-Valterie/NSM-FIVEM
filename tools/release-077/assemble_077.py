"""Assemble a fresh integrated v0.7.7 package; never edits the input release.
Usage: python assemble_077.py BASE_076.zip RELEASE_INPUT_DIR OUTPUT.zip
RELEASE_INPUT_DIR contains the rebuilt backend, version metadata, startup scripts,
release notes, source, test results, and the native server patch/config.
"""
from pathlib import Path
import hashlib
import io
import json
import sys
import zipfile


def sha(data):
    return hashlib.sha256(data).hexdigest()


def assemble(base, inputs, output):
    inputs = Path(inputs)
    if Path(base).resolve() == Path(output).resolve():
        raise ValueError('Output must not replace input')
    sys.path.insert(0, str(inputs / 'source'))
    from restore_auth import restore, ORIGINAL_SHA256, RESTORED_SHA256
    with zipfile.ZipFile(base) as source:
        original_auth = source.read('NSM-Client/legitimacy.dll')
        auth = restore(original_auth)
        original_ui = source.read('NSM-Client/citizen/ui.zip')
        if sha(original_ui) != 'ac5bdd1f926c7713786b87d226979ef55fa2e603471ccc51eb185641c3c50208':
            raise ValueError('Unexpected base UI ZIP')
        ui = io.BytesIO()
        old = b'init({dsn:`http://127.0.0.1/sentry-ingest`,tracesSampleRate:.05'
        new = b'init({enabled:!1,dsn:void 0,tracesSampleRate:0'
        count = 0
        with zipfile.ZipFile(io.BytesIO(original_ui)) as ui_in, zipfile.ZipFile(ui, 'w', zipfile.ZIP_DEFLATED) as ui_out:
            for info in ui_in.infolist():
                body = ui_in.read(info)
                count += body.count(old)
                ui_out.writestr(info, body.replace(old, new))
        if count != 1:
            raise ValueError('Unexpected Sentry initialization count')
        overlay = {
            'NSM-Client/legitimacy.dll': auth,
            'NSM-Client/citizen/ui.zip': ui.getvalue(),
            'NSM-Client/NSM-Masterlist.exe': (inputs / 'NSM-Masterlist.exe').read_bytes(),
            'NSM-Client/README-LOCALTEST.txt': (inputs / 'README-LOCALTEST.txt').read_bytes(),
            'NSM-Client/DIAGNOSTIC-NOTES.txt': b'NSM v0.7.7 local test. See RELEASE-NOTES.md for included fixes and test limits.\r\n',
        }
        for name in ('START-LOCALHOST.bat', 'START-LOCALHOST.ps1', 'START-LOCAL-FIRST.bat', 'VERSION.json', 'RELEASE-NOTES.md'):
            overlay['NSM-Client/' + name] = (inputs / name).read_bytes()
        for p in (inputs / 'source').rglob('*'):
            if p.is_file() and '__pycache__' not in p.parts:
                overlay['NSM-Client/repair-info/source/' + p.relative_to(inputs / 'source').as_posix()] = p.read_bytes()
        for name in ('go-tests.txt', 'race-tests.txt', 'ui-contract-tests.txt', 'local-server-heartbeat.patch', 'server-local.cfg', 'AUTH-STATIC-CHECKS.json'):
            overlay['NSM-Client/repair-info/' + name] = (inputs / name).read_bytes()
        overlay['NSM-Client/repair-info/assemble_077.py'] = Path(__file__).read_bytes()
        sums = {}
        with zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as result:
            for info in source.infolist():
                if info.filename in overlay or info.is_dir():
                    continue
                # Never carry runtime credentials, logs, or test state into a release.
                lower = info.filename.lower()
                if any(token in lower for token in ('/logs/', '/local-first-logs/', '/mirror-cache/', '/data/cache/')) or lower.endswith(('/accounts.json', '/state.json', '/ticket-signing-key.pem')):
                    continue
                data = source.read(info)
                result.writestr(info, data)
                sums[info.filename] = sha(data)
            for name, data in overlay.items():
                result.writestr(name, data)
                sums[name] = sha(data)
            manifest = ''.join(f'{digest}  {name}\n' for name, digest in sorted(sums.items()))
            result.writestr('NSM-Client/SHA256SUMS.txt', manifest)
    # Read every archive member back to verify integrity and the integration.
    with zipfile.ZipFile(output) as result:
        bad = result.testzip()
        if bad:
            raise ValueError('Corrupt archive member: ' + bad)
        if len(result.namelist()) != len(set(result.namelist())):
            raise ValueError('Duplicate ZIP entries')
        for name, digest in sums.items():
            if sha(result.read(name)) != digest:
                raise ValueError('Hash mismatch: ' + name)
        assert sha(result.read('NSM-Client/legitimacy.dll')) == RESTORED_SHA256
        assert result.read('NSM-Client/NSM-Masterlist.exe') == (inputs / 'NSM-Masterlist.exe').read_bytes()
        startup = result.read('NSM-Client/START-LOCALHOST.ps1')
        assert b'0.7.7-local' in startup and b'http://127.0.0.1:80' in startup
        assert json.loads(result.read('NSM-Client/VERSION.json'))['version'] == '0.7.7-localtest'
    report = {'archive': str(output), 'sha256': sha(Path(output).read_bytes()), 'size': Path(output).stat().st_size,
              'files_verified': len(sums), 'auth_candidate_matches_user_test': True, 'telemetry_patches': count,
              'windows_integrated_runtime_verified': False}
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    assemble(*sys.argv[1:])
