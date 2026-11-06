# Install cyrlm on Windows, then print the prompt to paste into your coding agent.
#   irm https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.ps1 | iex
# Environment: CYRLM_VERSION (default: latest), CYRLM_INSTALL_DIR, CYRLM_NO_PROMPT=1
$ErrorActionPreference = 'Stop'
$repo = 'aeroknight786/cyrlm'
$branch = 'ccr-db94bc6e-e47992'
$version = if ($env:CYRLM_VERSION) { $env:CYRLM_VERSION } else { 'latest' }
$dir = if ($env:CYRLM_INSTALL_DIR) { $env:CYRLM_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'cyrlm\bin' }
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$name = "cyrlm_windows_$arch.zip"
$base = if ($version -eq 'latest') { "https://github.com/$repo/releases/latest/download" } else { "https://github.com/$repo/releases/download/$version" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("cyrlm-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir 'cyrlm.exe'
try {
  try {
    Invoke-WebRequest -UseBasicParsing "$base/$name" -OutFile (Join-Path $tmp $name)
    Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')
    $expected = (Select-String -Path (Join-Path $tmp 'checksums.txt') -Pattern " $name$").Line.Split(' ')[0]
    $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $name)).Hash.ToLower()
    if ($expected -ne $actual) { throw "checksum mismatch for $name" }
    Expand-Archive -Path (Join-Path $tmp $name) -DestinationPath $tmp -Force
    Copy-Item (Join-Path $tmp "cyrlm_windows_$arch\cyrlm.exe") $exe -Force
  } catch {
    if (Get-Command go -ErrorAction SilentlyContinue) {
      Write-Host 'no release binary found; building with Go instead...'
      $env:GOBIN = $dir
      go install "github.com/$repo/cmd/cyrlm@$branch"
    } else {
      throw 'No cyrlm release is published yet and Go is not installed. Install Go (https://go.dev/dl) and rerun.'
    }
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

if ($env:CYRLM_NO_PROMPT -ne '1') {
  Write-Host ''
  Write-Host '──────── Paste everything below into your coding agent (Claude Code, Codex, OpenCode, …) ────────'
  Write-Host ''
  & $exe prompt
  Write-Host ''
  Write-Host '────────────────────────────────────────────────────────────────────────────────────────────'
}
