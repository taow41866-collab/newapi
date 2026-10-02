# Local-only acceptance. Does not push, deploy, start Docker or touch production.
$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '../..')
$go = 'D:/Go/go/bin/go.exe'
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
$env:GOWORK = 'off'
$testExec = 'E:/python/python.exe F:/Projects/newapi/tasks/model-routing/wsl-go-test-exec.py'
$output = Join-Path (Get-Location) 'build/model-routing'
New-Item -ItemType Directory -Force $output | Out-Null

# Fingerprint the exact source tree, including untracked implementation files.
function Get-SourceFingerprint {
    $paths = @(& git ls-files --cached --others --exclude-standard) |
        Where-Object { $_ -match '\.(go|mod|sum)$' } | Sort-Object -Unique
    $records = foreach ($path in $paths) {
        if (Test-Path -LiteralPath $path -PathType Leaf) {
            [ordered]@{ path = $path; sha256 = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash }
        }
    }
    return ($records | ConvertTo-Json -Compress)
}
$before = Get-SourceFingerprint

& $go test -count=1 -exec $testExec ./... *> "$output/linux-regression.log"
if ($LASTEXITCODE -ne 0) { throw "Linux tests failed: $output/linux-regression.log" }
Push-Location relaykit
try {
    & $go test -count=1 -exec $testExec ./... *> "$output/relaykit-regression.log"
    if ($LASTEXITCODE -ne 0) { throw 'relaykit tests failed' }
} finally { Pop-Location }
& $go build ./... *> "$output/linux-build.log"
if ($LASTEXITCODE -ne 0) { throw 'Linux build failed' }
& $go test -c -o "$output/model.test" ./model
if ($LASTEXITCODE -ne 0) { throw 'Database test build failed' }
# WSL may emit a nonfatal localhost/NAT warning on stderr while returning 0.
# Windows PowerShell treats that stderr as a terminating error under Stop.
try {
    $ErrorActionPreference = 'Continue'
    & wsl -d Ubuntu-2404 -u root --exec sh /mnt/f/Projects/newapi/tasks/model-routing/test-local-matrix.sh *> "$output/database-matrix.log"
    $matrixExit = $LASTEXITCODE
} finally {
    $ErrorActionPreference = 'Stop'
}
if ($matrixExit -ne 0) { throw 'Real database matrix failed' }
if ((Get-SourceFingerprint) -cne $before) { throw 'Source changed during acceptance; do not use this run as a frozen-source result.' }
$before | Set-Content -Encoding utf8 "$output/source-manifest.json"
Write-Output 'PASS: Linux backend, relaykit, build, affected real SQL matrix; source fingerprint unchanged.'
Write-Output 'Dedicated Redis/process/performance tests run separately; normal suite skips their env-gated entry points.'
