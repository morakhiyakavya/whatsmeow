param([switch]$Bootstrap)
$ErrorActionPreference = 'Stop'
$taskGoCommand = Get-Command go -ErrorAction SilentlyContinue
$taskGo = if ($taskGoCommand) { $taskGoCommand.Source } else { Join-Path $env:LOCALAPPDATA 'Programs/Go1.27.1/go/bin/go.exe' }
if (-not (Test-Path -LiteralPath $taskGo)) { throw 'Go 1.27.1 is required; install it or put go on PATH.' }
Push-Location (Join-Path $PSScriptRoot '../backend')
try {
    if ($Bootstrap) { & $taskGo run . -env ../.env -bootstrap } else { & $taskGo run . -env ../.env }
    if ($LASTEXITCODE -ne 0) { throw 'API command failed' }
} finally { Pop-Location }
