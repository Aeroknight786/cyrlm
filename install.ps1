# Install triaLM on Windows, then start it.
#   irm https://github.com/aeroknight786/trialm/releases/latest/download/install.ps1 | iex
# Environment: TRIALM_VERSION (default: latest), TRIALM_INSTALL_DIR (default: %LOCALAPPDATA%\trialm\bin),
#              TRIALM_NO_START=1 to install without starting trialm (for coding agents and scripts).
# The CYRLM_* names of these variables still work for one release.
$ErrorActionPreference = 'Stop'
$repo = 'aeroknight786/trialm'
function Get-Setting($name, $default) {
  $v = [Environment]::GetEnvironmentVariable("TRIALM_$name")
  if (-not $v) { $v = [Environment]::GetEnvironmentVariable("CYRLM_$name") }
  if ($v) { $v } else { $default }
}
$version = Get-Setting 'VERSION' 'latest'
$dir = Get-Setting 'INSTALL_DIR' (Join-Path $env:LOCALAPPDATA 'trialm\bin')
$noStart = Get-Setting 'NO_START' (Get-Setting 'NO_PROMPT' '')
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$name = "trialm_windows_$arch.zip"
$base = if ($version -eq 'latest') { "https://github.com/$repo/releases/latest/download" } else { "https://github.com/$repo/releases/download/$version" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("trialm-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir 'trialm.exe'
try {
  try {
    Invoke-WebRequest -UseBasicParsing "$base/$name" -OutFile (Join-Path $tmp $name)
    Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')
    $expected = (Select-String -Path (Join-Path $tmp 'checksums.txt') -Pattern " $name$").Line.Split(' ')[0]
    $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $name)).Hash.ToLower()
    if ($expected -ne $actual) { throw "checksum mismatch for $name" }
    Expand-Archive -Path (Join-Path $tmp $name) -DestinationPath $tmp -Force
    Copy-Item (Join-Path $tmp "trialm_windows_$arch\trialm.exe") $exe -Force
  } catch {
    if ($_.Exception.Message -like 'checksum mismatch*') { throw }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
      throw 'No trialm release binary was found and Go is not installed. Install Go 1.21 or newer (https://go.dev/dl) and rerun.'
    }
    $gover = (go env GOVERSION)
    $minor = 0
    if ($gover -match '^go1\.(\d+)') { $minor = [int]$Matches[1] }
    if ($minor -lt 21) {
      throw "No trialm release binary was found, and your Go ($gover) is too old to build it. Install Go 1.21 or newer (https://go.dev/dl) and rerun; it fetches the rest by itself."
    }
    Write-Host 'no release binary found; building with Go instead...'
    $env:GOBIN = $dir
    $env:GOTOOLCHAIN = 'auto'
    go install "github.com/$repo/cmd/trialm@latest"
  }
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not ($userPath -split ';' | Where-Object { $_ -eq $dir })) {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
  $env:Path = "$env:Path;$dir"
  Write-Host "added $dir to your PATH (new terminals pick it up)"
}
Write-Host "installed $exe ($(& $exe version))"

# cyrlm is now triaLM: an old cyrlm.exe becomes a shim, so old commands and
# setups keep working for one release.
$oldDir = Join-Path $env:LOCALAPPDATA 'cyrlm\bin'
foreach ($d in @($dir, $oldDir)) {
  $old = Join-Path $d 'cyrlm.exe'
  if (Test-Path $old) {
    Remove-Item -Force $old
    Set-Content -Path (Join-Path $d 'cyrlm.cmd') -Encoding ASCII -Value "@rem cyrlm is now triaLM; this shim goes away in v1.0.`r`n@`"$exe`" %*"
    Write-Host "replaced the old cyrlm.exe in $d with a shim that runs trialm"
  }
}

if ($noStart -ne '1' -and [Environment]::UserInteractive -and -not [Console]::IsInputRedirected) {
  Write-Host ''
  & $exe
} else {
  Write-Host 'run: trialm'
}
