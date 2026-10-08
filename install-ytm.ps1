param(
  [string]$InstallDirectory = (Join-Path $env:LOCALAPPDATA 'Programs\ytm'),
  [switch]$SkipDependencies,
  [switch]$NoPath
)

# Scope preferences/functions locally, including when invoked with irm | iex.
& {
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$phase = 'preflight'
$work = $null
$transcribing = $false
$oldTls = [Net.ServicePointManager]::SecurityProtocol

function Write-Step([string]$Message) {
  Write-Host "`n==> $Message" -ForegroundColor Cyan
}

function Receive-YtmFile([string]$Url, [string]$Destination) {
  $failure = ''
  for ($attempt = 1; $attempt -le 3; $attempt++) {
    try {
      Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Destination -TimeoutSec 120
      if ((Get-Item -LiteralPath $Destination).Length -eq 0) { throw 'Empty download.' }
      return
    } catch {
      $failure = $_.Exception.Message
      Write-Warning "Download attempt $attempt/3 failed: $failure"
      if ($attempt -lt 3) { Start-Sleep -Seconds (2 * $attempt) }
    }
  }
  $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
  if ($curl) {
    Write-Host 'Retrying with Windows curl...'
    try {
      & $curl.Source --fail --location --silent --show-error --connect-timeout 20 --max-time 240 --retry 2 --output $Destination $Url
      if ($LASTEXITCODE -eq 0 -and (Get-Item -LiteralPath $Destination).Length -gt 0) { return }
    } catch { $failure = $_.Exception.Message }
  }
  throw "Cannot download $Url. $failure"
}

try {
[Net.ServicePointManager]::SecurityProtocol = $oldTls -bor [Net.SecurityProtocolType]::Tls12
$logDir = Join-Path $env:LOCALAPPDATA 'ytm\logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$logPath = Join-Path $logDir ('install-' + [guid]::NewGuid().ToString('N') + '.log')
try {
  Start-Transcript -Path $logPath -Force | Out-Null
  $transcribing = $true
} catch { Write-Warning "Cannot create installer log: $($_.Exception.Message)" }
Write-Host "YTM installer | PowerShell $($PSVersionTable.PSVersion)"

if (-not $IsWindows -and $env:OS -ne 'Windows_NT') {
  throw 'YTM currently supports Windows 10/11 only.'
}

if (-not $SkipDependencies) {
$phase = 'dependency setup'
$winget = Get-Command winget.exe -ErrorAction SilentlyContinue
if (-not $winget) {
  throw 'WinGet is required to install CAVA. Install App Installer from Microsoft Store, then rerun this command.'
}

Write-Step 'Checking Scoop'
$scoop = Get-Command scoop -ErrorAction SilentlyContinue
if (-not $scoop) {
  Write-Host 'Installing Scoop for this Windows user (no administrator rights needed).' -ForegroundColor Gray
  $policy = Get-ExecutionPolicy -Scope CurrentUser
  if ($policy -eq 'Restricted' -or $policy -eq 'Undefined') {
    Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned -Force
  }
  Invoke-RestMethod -Uri 'https://get.scoop.sh' | Invoke-Expression
  $scoopShim = Join-Path $env:USERPROFILE 'scoop\shims'
  if (Test-Path -LiteralPath $scoopShim) { $env:PATH = "$scoopShim;$env:PATH" }
  $scoop = Get-Command scoop -ErrorAction SilentlyContinue
  if (-not $scoop) { throw 'Scoop installed, but its command is unavailable. Open a new PowerShell and rerun the installer.' }
}

Write-Step 'Installing playback dependencies'
# Scoop needs the Git program to add buckets; no GitHub account is required.
if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
  Write-Step 'Installing Git for Scoop'
  $global:LASTEXITCODE = 0
  & scoop install git
  if ($LASTEXITCODE -ne 0) { throw 'Scoop failed to install Git.' }
  $env:PATH = "$env:PATH;" + [Environment]::GetEnvironmentVariable('Path', 'User')
  if (-not (Get-Command git -ErrorAction SilentlyContinue)) { throw 'Git is unavailable. Open a new terminal and rerun the installer.' }
}
$global:LASTEXITCODE = 0
$buckets = @(& scoop bucket list 2>$null | Select-Object -ExpandProperty Name)
if ($LASTEXITCODE -ne 0) { throw 'Could not read Scoop buckets.' }
if ($buckets -notcontains 'extras') {
  & scoop bucket add extras
  if ($LASTEXITCODE -ne 0) { throw 'Scoop failed to add its extras bucket, which provides mpv.' }
}
if (-not (Get-Command mpv -ErrorAction SilentlyContinue)) {
  & scoop install mpv
  if ($LASTEXITCODE -ne 0) { throw 'Scoop failed to install mpv.' }
}
if (-not (Get-Command yt-dlp -ErrorAction SilentlyContinue)) {
  & scoop install yt-dlp
  if ($LASTEXITCODE -ne 0) { throw 'Scoop failed to install yt-dlp.' }
}
if (-not (Get-Command deno -ErrorAction SilentlyContinue)) {
  & scoop install deno
  if ($LASTEXITCODE -ne 0) { throw 'Scoop failed to install Deno.' }
}
if (-not (Get-Command cava -ErrorAction SilentlyContinue) -and
    -not (Test-Path -LiteralPath (Join-Path $env:LOCALAPPDATA 'cava\cava.exe'))) {
  & $winget.Source install --id karlstav.cava -e --accept-source-agreements --accept-package-agreements
  if ($LASTEXITCODE -ne 0) { throw 'WinGet failed to install CAVA.' }
}

}

$phase = 'downloading YTM ZIP'
Write-Step 'Downloading YTM ZIP'
$work = Join-Path ([IO.Path]::GetTempPath()) ('ytm-install-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
  $zip = Join-Path $work 'ytm-windows-amd64.zip'
  $checksum = "$zip.sha256"
  Receive-YtmFile 'https://github.com/jkvhvjhkjhx/ytm/releases/latest/download/ytm-windows-amd64.zip' $zip
  $phase = 'downloading checksum'
  Write-Step 'Downloading checksum'
  Receive-YtmFile 'https://github.com/jkvhvjhkjhx/ytm/releases/latest/download/ytm-windows-amd64.zip.sha256' $checksum
  $phase = 'verifying SHA256'
  Write-Step 'Verifying SHA256'
  $expected = ((Get-Content -LiteralPath $checksum -Raw).Trim() -split '\s+')[0].ToLowerInvariant()
  $actual = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($expected -notmatch '^[0-9a-f]{64}$' -or $actual -ne $expected) {
    throw 'YTM package checksum did not match; downloaded files were not installed.'
  }
  $phase = 'extracting YTM'
  Write-Step 'Extracting YTM'
  $extract = Join-Path $work 'package'
  Expand-Archive -LiteralPath $zip -DestinationPath $extract
  $binaries = @(Get-ChildItem -LiteralPath $extract -Filter ytm.exe -File -Recurse)
  if ($binaries.Count -ne 1) { throw 'Expected exactly one ytm.exe in the release archive.' }
  $binary = $binaries[0]

  # Install verified files directly instead of executing a second script from the ZIP.
  $phase = 'installing files (close YTM if it is already running)'
  Write-Step 'Installing YTM files'
  $target = [IO.Path]::GetFullPath($InstallDirectory)
  New-Item -ItemType Directory -Force -Path $target | Out-Null
  Get-ChildItem -LiteralPath $binary.DirectoryName | ForEach-Object {
    Copy-Item -LiteralPath $_.FullName -Destination $target -Recurse -Force
  }
  $phase = 'checking installed executable'
  Write-Step 'Checking installed executable'
  $installedExe = Join-Path $target 'ytm.exe'
  $version = & $installedExe --version
  if ($LASTEXITCODE -ne 0 -or "$version" -notmatch '^ytm ') { throw 'Installed ytm.exe could not start.' }
  Write-Host $version -ForegroundColor Green
  if (-not $NoPath) {
    $phase = 'registering the ytm command'
    Write-Step 'Registering the ytm command'
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $parts = @($userPath -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ine $target.TrimEnd('\') })
    [Environment]::SetEnvironmentVariable('Path', ((@($target) + $parts) -join ';'), 'User')
    $env:PATH = "$target;$env:PATH"
  }
  Write-Host "`nYTM installed at $target" -ForegroundColor Green
  if ($NoPath) { Write-Host "Command registration skipped. Run: & '$installedExe'" }
  else { Write-Host 'Run: ytm (or open a new Windows Terminal and run ytm)' -ForegroundColor Green }
  if ($SkipDependencies) { Write-Host 'Dependency setup was skipped. Use ytm --doctor to check.' }
} catch {
  $failure = $_.Exception.Message
  Write-Host "`nYTM installation failed at: $phase" -ForegroundColor Red
  if ($transcribing) { Write-Host "Log: $logPath" }
  throw $failure
} finally {
  [Net.ServicePointManager]::SecurityProtocol = $oldTls
  if ($work -and (Test-Path -LiteralPath $work)) {
    $resolvedWork = [IO.Path]::GetFullPath($work)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\')
    if ((Split-Path -Parent $resolvedWork).TrimEnd('\') -ieq $tempRoot -and
        (Split-Path -Leaf $resolvedWork) -match '^ytm-install-[0-9a-f]{32}$') {
      Remove-Item -LiteralPath $resolvedWork -Recurse -Force -ErrorAction SilentlyContinue
    }
  }
  if ($transcribing) {
    Write-Host "Installer log: $logPath" -ForegroundColor Gray
    try { Stop-Transcript | Out-Null } catch { }
  }
}

}
