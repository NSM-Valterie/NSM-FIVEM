NSM v0.7.7 LOCAL TEST — INTEGRATED REPAIR
======================================

Extract into a NEW folder. Keep your working v0.7.0 install unchanged.
Run START-LOCALHOST.bat, not an older startup shortcut.
Stop an old NSM backend manually if it already owns port 80.
Steam and Rockstar should remain signed in as your normal user.

Included: user-confirmed launch rollback, repaired local API/backend on port 80,
startup version/PID checks, and disabled invalid Sentry telemetry.
No separate repair installer is needed. No sample servers are preloaded.

The launch DLL is the EXACT candidate you confirmed working. Its three
entitlement endpoints use the original v0.7.0 HTTPS service; legitimate ownership
checks remain active. Server-browser APIs stay at http://127.0.0.1:80.

The complete combined ZIP still needs a Windows test. Local game-server listing
and connection are NOT yet verified end to end. Backend logs and verified health
are written to local-first-logs. Share those plus the newest CitizenFX log if needed.

See RELEASE-NOTES.md, VERSION.json, and repair-info for source/tests/details.
Old v0.7.5 diagnostic/readme files were replaced to avoid conflicting instructions.
Native client file-version resources remain from the v0.7.6 baseline; this is a
repack with a rebuilt backend, not an untested claim of a full native rebuild.
