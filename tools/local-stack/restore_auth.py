"""Restore the three original v0.7.0 entitlement endpoints in the exact v0.7.6 DLL.
This preserves code bytes, NSM subprocess names, and authentication checks.
Usage: python restore_auth.py input-legitimacy.dll output-legitimacy.dll
"""
import hashlib
import sys
from pathlib import Path

ORIGINAL_SHA256 = '1f10baf67e36d4e624705a08af72f843db09668b7b6fcee5c2a4a446412a0282'
RESTORED_SHA256 = 'fb62755f2e7aadabfe69d3d3299701d31b2e8ce98e1f9da68867a199b8f9be14'


def restore(data):
    if hashlib.sha256(data).hexdigest() != ORIGINAL_SHA256:
        raise ValueError('Input is not the verified v0.7.6 DLL')
    result = data
    for suffix in ('', '/rosfive', '/ros2'):
        local = ('http://127.0.0.1/l/api/validate/entitlement' + suffix).encode()
        upstream = ('https://lambda.fivem.net/api/validate/entitlement' + suffix).encode()
        old = local + b'\0' * (len(upstream) - len(local) + 1)
        new = upstream + b'\0'
        if result.count(old) != 1:
            raise ValueError('Unexpected endpoint count')
        result = result.replace(old, new)
    if len(result) != len(data) or hashlib.sha256(result).hexdigest() != RESTORED_SHA256:
        raise ValueError('Output differs from user-confirmed launch candidate')
    return result


if __name__ == '__main__':
    if Path(sys.argv[1]).resolve() == Path(sys.argv[2]).resolve():
        raise ValueError('Use distinct input and output files')
    Path(sys.argv[2]).write_bytes(restore(Path(sys.argv[1]).read_bytes()))
