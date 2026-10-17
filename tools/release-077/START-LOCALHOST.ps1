$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$exe = Join-Path $PSScriptRoot 'NSM-Masterlist.exe'
$logDir = Join-Path $PSScriptRoot 'local-first-logs'
New-Item -ItemType Directory -Path $logDir -Force | Out-Null
function Get-LocalJson([string]$Path, [string]$Method = 'GET') {
    $request = [System.Net.HttpWebRequest]::Create('http://127.0.0.1:80' + $Path)
    $request.Proxy = $null
    $request.Method = $Method
    $request.Timeout = 2000
    if ($Method -eq 'POST') { $request.ContentLength = 0 }
    $response = $request.GetResponse()
    try {
        if ($response.Headers['X-NSM-Backend'] -ne '0.7.7-local') { throw 'Wrong backend signature on port 80.' }
        $reader = New-Object System.IO.StreamReader($response.GetResponseStream())
        try { return ($reader.ReadToEnd() | ConvertFrom-Json) } finally { $reader.Dispose() }
    } finally { $response.Dispose() }
}
function Show-PortOwner {
    Write-Host 'Port 80 listener information (nothing will be killed automatically):'
    try {
        Get-NetTCPConnection -State Listen -LocalPort 80 -ErrorAction Stop | ForEach-Object {
            $owner = Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue
            Write-Host ("  Address={0} PID={1} Process={2} Path={3}" -f $_.LocalAddress,$_.OwningProcess,$owner.ProcessName,$owner.Path)
        }
    } catch { & netstat.exe -ano | Select-String ':80\s' | ForEach-Object { Write-Host $_ } }
}
try {
    if (!(Test-Path $exe) -or !(Test-Path (Join-Path $PSScriptRoot 'NSM.exe'))) { throw 'Place this package in the complete v0.7.6 NSM-Client folder.' }
    $auth = Join-Path $PSScriptRoot 'legitimacy.dll'
    if (!(Test-Path $auth) -or (Get-FileHash $auth -Algorithm SHA256).Hash.ToLowerInvariant() -ne 'fb62755f2e7aadabfe69d3d3299701d31b2e8ce98e1f9da68867a199b8f9be14') { throw 'Launch component does not match the tested v0.7.7 authentication rollback. Use a fresh extraction.' }
    # Only bypass proxies for loopback. Do not alter Windows settings or external ownership validation.
    $env:NO_PROXY = (($env:NO_PROXY + ',localhost,127.0.0.1,::1').Trim(','))
    $env:no_proxy = $env:NO_PROXY
    Show-PortOwner
    $health = $null
    try { $health = Get-LocalJson '/health' } catch { }
    if (!$health) {
        $listeners = @(Get-NetTCPConnection -State Listen -LocalPort 80 -ErrorAction SilentlyContinue)
        if ($listeners.Count -gt 0) { throw 'Port 80 is occupied by an unverified service. Stop only the old NSM backend or resolve the conflicting service, then retry.' }
        $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
        $process = Start-Process -FilePath $exe -ArgumentList @('-bind','127.0.0.1','-port','80') -WorkingDirectory $PSScriptRoot -PassThru -RedirectStandardOutput (Join-Path $logDir "$stamp-stdout.txt") -RedirectStandardError (Join-Path $logDir "$stamp-backend.txt")
        for ($i=0; $i -lt 20; $i++) {
            try { $health = Get-LocalJson '/health'; if ($health) { break } } catch { }
            if ($process.HasExited) { throw "Backend exited. Read local-first-logs\$stamp-backend.txt." }
            Start-Sleep -Seconds 1
        }
    }
    if (!$health -or $health.service -ne 'nsm-local-first' -or $health.version -ne '0.7.7-local') { throw 'Expected local-first backend health/version not found.' }
    $owner = Get-Process -Id $health.pid -ErrorAction Stop
    if (!$owner.Path -or [IO.Path]::GetFullPath($owner.Path) -ne [IO.Path]::GetFullPath($exe)) { throw 'Health PID is not the repair executable in this folder. Refusing to use a stale backend.' }
    if ((Get-FileHash $owner.Path).Hash -ne (Get-FileHash $exe).Hash) { throw 'Backend executable hash mismatch.' }
    $identity = Get-LocalJson '/api/identities'
    $linked = Get-LocalJson '/api/ticket/identities' 'POST'
    $featured = Get-LocalJson '/v1/public/featured-servers'
    if ($null -eq $identity.identities -or $null -eq $linked.identities -or $null -eq $featured.pinnedServers) { throw 'Backend identity/featured contract check failed.' }
    $health | ConvertTo-Json -Depth 6 | Set-Content (Join-Path $logDir 'verified-health.json')
    Write-Host 'Verified NSM local-first backend on 127.0.0.1:80. Starting the unmodified NSM client.'
    Start-Process -FilePath (Join-Path $PSScriptRoot 'NSM.exe') -WorkingDirectory $PSScriptRoot
} catch {
    Show-PortOwner
    Write-Host $_.Exception.Message -ForegroundColor Red
    $_.Exception.Message | Set-Content (Join-Path $logDir 'startup-error.txt')
    exit 1
}
