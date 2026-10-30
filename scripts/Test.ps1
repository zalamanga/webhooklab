[CmdletBinding()]
param([switch]$WithBrowser)
. (Join-Path $PSScriptRoot 'Common.ps1')
& (Join-Path $PSScriptRoot 'Init.ps1')
$webhookValues = Get-WebhookEnvironment
if (Get-NetTCPConnection -State Listen -LocalPort 5449 -ErrorAction SilentlyContinue) { throw 'Port 5449 sudah dipakai. Test tidak memakai database yang sedang berjalan.' }
$webhookNames = @('WEBHOOK_TEST_DB_URL','WEBHOOK_DEMO_PASSWORD','WEBHOOK_BASE_URL','WEBHOOK_TEST_DB_PASSWORD','npm_config_cache','NODE_OPTIONS')
$webhookPrevious = @{}
foreach ($webhookName in $webhookNames) { $webhookPrevious[$webhookName] = [Environment]::GetEnvironmentVariable($webhookName,'Process') }
$webhookTestCompose = @('compose','-f','compose.test.yml','-p','webhooklab-test')
$webhookTestStarted = $false
Push-Location $script:WebhookRoot
try {
    $env:WEBHOOK_TEST_DB_PASSWORD = $webhookValues['WEBHOOK_TEST_DB_PASSWORD']
    $env:WEBHOOK_TEST_DB_URL = 'postgres://webhooklab_test:' + $webhookValues['WEBHOOK_TEST_DB_PASSWORD'] + '@127.0.0.1:5449/webhooklab_test?sslmode=disable'
    $webhookTestStarted = $true
    Invoke-WebhookDocker -Arguments ($webhookTestCompose + @('up','-d','--wait','--wait-timeout','90'))
    Push-Location (Join-Path $script:WebhookRoot 'backend')
    try { & (Join-Path $PSScriptRoot 'Go.ps1') -Arguments @('test','-count=1','-v','./...') } finally { Pop-Location }
    $env:npm_config_cache = Join-Path $script:WebhookRoot '.local/npm-cache'
    $env:NODE_OPTIONS = '--max-old-space-size=512'
    Push-Location (Join-Path $script:WebhookRoot 'frontend')
    try {
        if (-not (Test-Path -LiteralPath 'node_modules/.package-lock.json')) {
            & npm.cmd ci --ignore-scripts
            if ($LASTEXITCODE -ne 0) { throw 'Instalasi frontend gagal.' }
        }
        & npm.cmd test
        if ($LASTEXITCODE -ne 0) { throw 'Tes frontend gagal.' }
        & npm.cmd run build
        if ($LASTEXITCODE -ne 0) { throw 'Build Next.js gagal.' }
        & node generate-csp.mjs
        if ($LASTEXITCODE -ne 0) { throw 'Pembuatan header CSP gagal.' }
        if ($WithBrowser) {
            $env:WEBHOOK_BASE_URL = 'http://127.0.0.1:8097'
            $env:WEBHOOK_DEMO_PASSWORD = $webhookValues['WEBHOOK_DEMO_PASSWORD']
            & npm.cmd run test:e2e
            if ($LASTEXITCODE -ne 0) { throw 'Tes browser gagal.' }
        }
    } finally { Pop-Location }
} finally {
    try {
        if ($webhookTestStarted) { Invoke-WebhookDocker -Arguments ($webhookTestCompose + @('down')) }
    } finally {
        foreach ($webhookName in $webhookNames) { [Environment]::SetEnvironmentVariable($webhookName,$webhookPrevious[$webhookName],'Process') }
        Pop-Location
    }
}
Write-Output 'Tes backend/PostgreSQL dan frontend selesai.'
