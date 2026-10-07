$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Write-Step([string]$Message) {
  Write-Host "`n==> $Message" -ForegroundColor Cyan
}

if (-not $IsWindows -and $env:OS -ne 'Windows_NT') {
  throw 'YTM currently supports Windows 10/11 only.'
}

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

Write-Step 'Downloading the latest YTM release'
$work = Join-Path ([IO.Path]::GetTempPath()) ('ytm-install-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
try {
  $zip = Join-Path $work 'ytm-windows-amd64.zip'
  $checksum = "$zip.sha256"
  Invoke-WebRequest -Uri 'https://github.com/jkvhvjhkjhx/ytm/releases/latest/download/ytm-windows-amd64.zip' -OutFile $zip
  Invoke-WebRequest -Uri 'https://github.com/jkvhvjhkjhx/ytm/releases/latest/download/ytm-windows-amd64.zip.sha256' -OutFile $checksum
  $expected = ((Get-Content -LiteralPath $checksum -Raw).Trim() -split '\s+')[0].ToLowerInvariant()
  $actual = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($expected -notmatch '^[0-9a-f]{64}$' -or $actual -ne $expected) {
    throw 'YTM package checksum did not match; downloaded files were not installed.'
  }
  $extract = Join-Path $work 'package'
  Expand-Archive -LiteralPath $zip -DestinationPath $extract
  $binary = Get-ChildItem -LiteralPath $extract -Filter ytm.exe -File -Recurse | Select-Object -First 1
  if (-not $binary) { throw 'The release archive did not contain ytm.exe.' }
  $installer = Join-Path $binary.DirectoryName 'install.ps1'
  if (-not (Test-Path -LiteralPath $installer)) { throw 'The release archive did not contain install.ps1.' }
  & $installer -Binary $binary.FullName
  if ($LASTEXITCODE -and $LASTEXITCODE -ne 0) { throw 'YTM installation failed.' }
} finally {
  Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host "`nYTM is installed. Close and reopen Windows Terminal, then run: ytm" -ForegroundColor Green
Write-Host 'Check dependencies any time with: ytm --doctor' -ForegroundColor Gray
