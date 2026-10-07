param(
  [string]$Go = 'go',
  [string]$OutputDirectory = (Join-Path $PSScriptRoot 'dist')
)
$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
  New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
  $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
  $name = "ytm-windows-amd64-$stamp"
  $stage = Join-Path $OutputDirectory $name
  New-Item -ItemType Directory -Path $stage -ErrorAction Stop | Out-Null
  $oldGOOS, $oldGOARCH = $env:GOOS, $env:GOARCH
  try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    & $Go build -trimpath -buildvcs=false -o (Join-Path $stage 'ytm.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
  } finally {
    $env:GOOS, $env:GOARCH = $oldGOOS, $oldGOARCH
  }
  foreach ($file in @('README.md','config.example.toml','install.ps1','THIRD-PARTY.md')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot $file) -Destination $stage
  }
  if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'LICENSE')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'LICENSE') -Destination $stage
  }
  $modules = @(& $Go list -buildvcs=false -deps -f '{{if .Module}}{{.Module.Path}}|{{.Module.Dir}}|{{.Module.Version}}{{end}}' . |
    Where-Object { $_ } | Sort-Object -Unique)
  if ($LASTEXITCODE -ne 0) { throw 'Cannot read dependency metadata.' }
  $notices = Join-Path $stage 'THIRD_PARTY_LICENSES'
  New-Item -ItemType Directory -Path $notices | Out-Null
  $goRoot = & $Go env GOROOT
  if ($LASTEXITCODE -ne 0) { throw 'Cannot locate Go runtime license.' }
  Copy-Item -LiteralPath (Join-Path $goRoot 'LICENSE') -Destination (Join-Path $notices 'Go-LICENSE')
  $publicModules = @()
  foreach ($module in $modules) {
    $parts = $module -split '\|', 3
    if ($parts.Count -lt 3 -or [string]::IsNullOrWhiteSpace($parts[2])) { continue }
    $publicModules += ($parts[0] + ' ' + $parts[2])
    $licenses = @(Get-ChildItem -LiteralPath $parts[1] -File | Where-Object { $_.Name -match '^(LICENSE|COPYING|NOTICE)' })
    if ($licenses.Count -eq 0 -and $parts[0] -eq 'github.com/mattn/go-localereader') {
      # This upstream version declares MIT and credits its author in README.md.
      $licenses = @(Get-Item -LiteralPath (Join-Path $parts[1] 'README.md'))
    }
    if ($licenses.Count -eq 0) { throw "No license found for $($parts[0]); inspect before distribution." }
    $moduleFolder = Join-Path $notices ($parts[0] -replace '[^A-Za-z0-9._-]', '_')
    New-Item -ItemType Directory -Path $moduleFolder | Out-Null
    $licenses | Copy-Item -Destination $moduleFolder
  }
  $publicModules | Set-Content -LiteralPath (Join-Path $notices 'MODULES.txt') -Encoding UTF8
  $zip = Join-Path $OutputDirectory ($name + '.zip')
  Compress-Archive -LiteralPath $stage -DestinationPath $zip
  $hash = Get-FileHash -LiteralPath $zip -Algorithm SHA256
  ($hash.Hash.ToLowerInvariant() + '  ' + (Split-Path -Leaf $zip)) |
    Set-Content -LiteralPath ($zip + '.sha256') -Encoding ASCII
  Get-Item -LiteralPath $zip, ($zip + '.sha256') | Select-Object FullName, Length
} finally {
  Pop-Location
}
