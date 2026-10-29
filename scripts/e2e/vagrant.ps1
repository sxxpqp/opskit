$ErrorActionPreference = 'Stop'
# Reuse the existing local box cache when it is present.
$taskRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
if (-not $env:VAGRANT_HOME) {
    $env:VAGRANT_HOME = Join-Path $taskRoot '.opskit/vagrant/home'
}
$env:TEMP = Join-Path $taskRoot 'dist/.vagrant-tmp'
$env:TMP = $env:TEMP
$env:TMPDIR = $env:TEMP
New-Item -ItemType Directory -Path $env:TEMP -Force | Out-Null
Push-Location $PSScriptRoot
try {
    & 'vagrant' @args
    $taskExitCode = $LASTEXITCODE
} finally { Pop-Location }
exit $taskExitCode
