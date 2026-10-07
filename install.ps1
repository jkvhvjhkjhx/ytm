param([string]$Binary = (Join-Path $PSScriptRoot 'ytm.exe'))
$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $Binary)) { throw "Không tìm thấy $Binary. Hãy build hoặc tải ytm.exe vào thư mục này." }
$installDir = Join-Path $env:LOCALAPPDATA 'Programs\ytm'
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
Copy-Item -LiteralPath $Binary -Destination (Join-Path $installDir 'ytm.exe') -Force
$scoop = Get-Command scoop -ErrorAction SilentlyContinue
if ($scoop) {
  & scoop shim add ytm (Join-Path $installDir 'ytm.exe')
  if ($LASTEXITCODE -ne 0) { throw 'Scoop không thể đăng ký lệnh ytm.' }
  Write-Host 'Registered ytm in Scoop shims.' -ForegroundColor DarkGray
} else {
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $parts = @($userPath -split ';' | Where-Object { $_ })
  if ($parts -notcontains $installDir) {
    try {
      [Environment]::SetEnvironmentVariable('Path', (($parts + $installDir) -join ';'), 'User')
    } catch {
      throw "Không thể thêm PATH người dùng. Thư mục cài: $installDir. Hãy thêm thư mục này vào PATH rồi mở PowerShell mới."
    }
  }
}
Write-Host "YTM installed at $installDir" -ForegroundColor Cyan
Write-Host 'Open a new PowerShell window, then run: ytm' -ForegroundColor Green
Write-Host 'The first launch offers to install mpv and yt-dlp with Scoop if missing.' -ForegroundColor DarkGray
