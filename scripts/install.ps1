#Requires -Version 5.1
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$Repository = if ($env:SATCHEL_REPOSITORY) { $env:SATCHEL_REPOSITORY } else { 'ByteTrue/satchel' }
$Version = if ($env:SATCHEL_VERSION) { $env:SATCHEL_VERSION } else { 'latest' }
$LocalAppData = if ($env:LOCALAPPDATA) { $env:LOCALAPPDATA } else { Join-Path $HOME 'AppData\Local' }
$InstallDir = if ($env:SATCHEL_INSTALL_DIR) { $env:SATCHEL_INSTALL_DIR } else { Join-Path $LocalAppData 'Programs\Satchel' }
$DownloadBase = $env:SATCHEL_DOWNLOAD_BASE

$Architecture = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
switch ($Architecture) {
    'AMD64' { $AssetArch = 'amd64' }
    'ARM64' { $AssetArch = 'arm64' }
    default { throw "Unsupported CPU architecture: $Architecture" }
}

function Get-SatchelFile([string]$Uri, [string]$OutFile) {
    if ([IO.File]::Exists($Uri)) {
        Copy-Item -Path $Uri -Destination $OutFile -Force
    } else {
        Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing -Headers @{ 'User-Agent' = 'satchel-installer' }
    }
}

$Work = Join-Path ([IO.Path]::GetTempPath()) ("satchel-install-" + [Guid]::NewGuid().ToString('N'))
$Staging = $null
New-Item -ItemType Directory -Path $Work -Force | Out-Null
try {
    if (-not $DownloadBase) {
        if ($Version -eq 'latest') {
            $Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repository/releases/latest" -Headers @{ 'User-Agent' = 'satchel-installer' }
            $Version = $Release.tag_name
        }
        if ($Version -notmatch '^v[A-Za-z0-9._-]+$') {
            throw "Invalid release tag: $Version"
        }
        $DownloadBase = "https://github.com/$Repository/releases/download/$Version"
    }

    $Asset = "satchel_windows_$AssetArch.zip"
    $Archive = Join-Path $Work $Asset
    $Checksums = Join-Path $Work 'SHA256SUMS'
    Write-Host "Downloading $Asset"
    Get-SatchelFile "$DownloadBase/$Asset" $Archive
    Get-SatchelFile "$DownloadBase/SHA256SUMS" $Checksums

    $EscapedAsset = [Regex]::Escape($Asset)
    $Line = Get-Content $Checksums | Where-Object { $_ -match "\s+\*?$EscapedAsset$" } | Select-Object -First 1
    if (-not $Line) { throw "SHA256SUMS has no entry for $Asset" }
    $Expected = ($Line -split '\s+')[0].ToLowerInvariant()
    if ($Expected -notmatch '^[0-9a-f]{64}$') { throw "Invalid checksum for $Asset" }
    $Actual = (Get-FileHash -Path $Archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected) { throw "Checksum mismatch for $Asset; refusing to install" }

    Expand-Archive -Path $Archive -DestinationPath $Work -Force
    $Binary = Join-Path $Work 'satchel.exe'
    if (-not (Test-Path $Binary -PathType Leaf)) { throw 'Release archive does not contain satchel.exe' }
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $Staging = Join-Path $InstallDir ".satchel.tmp.$PID.exe"
    Copy-Item -Path $Binary -Destination $Staging -Force
    Move-Item -Path $Staging -Destination (Join-Path $InstallDir 'satchel.exe') -Force
    $Staging = $null

    & (Join-Path $InstallDir 'satchel.exe') version
    if ($LASTEXITCODE -ne 0) { throw 'Installed binary failed its version check' }
    Write-Host "Installed satchel to $(Join-Path $InstallDir 'satchel.exe')"
    $UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (($UserPath -split ';') -notcontains $InstallDir) {
        $EscapedInstallDir = $InstallDir.Replace("'", "''")
        Write-Host "Add it to your user PATH, then open a new terminal:"
        Write-Host "`$path = [Environment]::GetEnvironmentVariable('Path', 'User')"
        Write-Host "[Environment]::SetEnvironmentVariable('Path', `$path + ';$EscapedInstallDir', 'User')"
    }
    Write-Host 'Next: satchel setup'
}
finally {
    if ($Staging) { Remove-Item -Path $Staging -Force -ErrorAction SilentlyContinue }
    Remove-Item -Path $Work -Recurse -Force -ErrorAction SilentlyContinue
}
