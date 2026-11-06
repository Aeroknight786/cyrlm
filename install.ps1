# cyrlm is now triaLM. This branch's installer forwards to the new one:
#   irm https://github.com/aeroknight786/trialm/releases/latest/download/install.ps1 | iex
# It installs `trialm`, turns an old `cyrlm` into a shim, and keeps your key and setups.
$ErrorActionPreference = 'Stop'
Write-Host 'cyrlm is now triaLM: installing trialm from https://github.com/aeroknight786/trialm'
Invoke-RestMethod https://github.com/aeroknight786/trialm/releases/latest/download/install.ps1 | Invoke-Expression
