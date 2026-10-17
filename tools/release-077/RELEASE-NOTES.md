# NSM v0.7.7 Local Test

Integrated maintenance package based on `NSM-client-v0.7.6-diagnostic-localtest.zip`.

## Included

- **Launch repair confirmed by the user:** restores the three entitlement-validation HTTPS URLs from working v0.7.0. The exact DLL SHA256 is `fb62755f2e7aadabfe69d3d3299701d31b2e8ce98e1f9da68867a199b8f9be14`. Executable-code bytes and authentication checks are unchanged.
- **Rebuilt local backend:** version `0.7.7-local`, localhost port 80, explicit UI API routes, correct identities/featured/notices schemas, validated heartbeats, framed protobuf lists, expiry and real counts. No sample servers or fake players are preloaded.
- **Startup verification:** `START-LOCALHOST.bat` starts/checks the correct backend, verifies its version/PID/path and the tested launch DLL, and reports port conflicts rather than killing unknown processes.
- **Telemetry disabled:** removes the invalid Sentry DSN through a guarded UI-only patch.
- Source, tests, static checks, and a native server-source patch/config for local heartbeats are supplied.

## Run

1. Extract the **complete ZIP into a new folder**. Do not merge into the working v0.7.0 install or an old v0.7.6 folder.
2. Close the old NSM backend manually if it owns port 80. Keep Steam and Rockstar signed in normally.
3. Run `NSM-Client/START-LOCALHOST.bat`. Do not apply either of the earlier repair installers: their changes are already integrated.
4. Backend details/errors are written to `NSM-Client/local-first-logs`.

The active backend is now `NSM-Masterlist.exe`. There is no second duplicate backend executable to choose between. `START-LOCAL-FIRST.bat` is an alias for the new startup for compatibility.

## Validation and limits

- 11 Go tests and 20 route subtests pass; Go race checks and vet pass.
- Windows x64 backend cross-compiled successfully.
- The actual v0.7.6 UI worker was tested against this backend over loopback HTTP: heartbeat, protobuf decode, game filter, server-view transform and UI message output pass with a clearly labeled test fixture.
- The user confirmed that the exact included authentication rollback fixes launching on their Windows/Steam setup.
- **The complete integrated v0.7.7 ZIP has not yet been run on Windows. A real server-browser-to-gameplay connection is not yet verified.**
- Native client EXE/subprocesses are the v0.7.6 baseline, not a newly compiled native client. The package version and backend version are v0.7.7; native Windows file-version resources are not changed.
- Native server is not included or rebuilt. Review `server-local.cfg` and `local-server-heartbeat.patch`; an older server can still send a heartbeat to its compiled default master even after a runtime override.
- External legitimate Rockstar/Steam validation remains required. Original authentication service access is intentionally restored, not redirected to a local substitute. Game-cache misses still use the existing CDN proxy. Local API operation is not an offline ownership solution.
- Identity linking and authenticated boosts are explicitly unavailable locally. Ticket public key requests return an explanatory 503 unless a trusted key is configured; no fake credentials or tickets are issued. Missing icons and unregistered server records deliberately return 404. Local update checks deliberately return 404 to keep this build.

No production service or existing release was overwritten. Keep v0.7.0 unchanged as a fallback. If needed, roll back by closing this folder's processes and returning to your old installation; keep account/cache data backed up and never publish them with diagnostics.
