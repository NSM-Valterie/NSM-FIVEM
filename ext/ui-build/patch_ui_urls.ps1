# NSM: rewrites the Cfx service/masterlist URLs inside the prebuilt UI bundle
# (the bundle is closed-source; string replacement in its JS is the only way
# to redirect it). Run by build.cmd after the bundle is copied to data.zip.
param(
    [Parameter(Mandatory = $true)][string]$ZipPath,
    [string]$NewBase = "http://194.104.146.133"
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression.FileSystem

$replacements = [ordered]@{
    'https://frontend.cfx-services.net' = $NewBase
    'https://lambda.fivem.net'          = $NewBase
    'https://servers-frontend.fivem.net'= $NewBase
    'https://gss.cfx-services.net'      = $NewBase
    'https://static.cfx.re/runtime/counts.json' = "$NewBase/runtime/counts.json"
}

$zip = [System.IO.Compression.ZipFile]::Open($ZipPath, 'Update')
try {
    $patched = 0
    foreach ($entry in @($zip.Entries)) {
        if ($entry.FullName -notmatch '\.(js|html|json|css)$') { continue }
        $reader = New-Object System.IO.StreamReader($entry.Open(), [System.Text.Encoding]::UTF8)
        $content = $reader.ReadToEnd()
        $reader.Close()

        $original = $content
        foreach ($key in $replacements.Keys) {
            $content = $content.Replace($key, $replacements[$key])
        }
        if ($content -ne $original) {
            $fullName = $entry.FullName
            $entry.Delete()
            $newEntry = $zip.CreateEntry($fullName)
            # UTF8Encoding($false) = no BOM (a BOM would also be fine for JS,
            # but keep the patched bytes as close to original as possible)
            $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
            $writer = New-Object System.IO.StreamWriter($newEntry.Open(), $utf8NoBom)
            $writer.Write($content)
            $writer.Close()
            $patched++
            Write-Host "patch_ui_urls: rewrote $fullName"
        }
    }
    Write-Host "patch_ui_urls: $patched file(s) patched in $ZipPath"
}
finally {
    $zip.Dispose()
}
