[CmdletBinding()]
param()
. (Join-Path $PSScriptRoot 'Common.ps1')
$webhookValues = Get-WebhookEnvironment
$webhookPreviousPassword = $env:WEBHOOK_DEMO_PASSWORD
$webhookPreviousBase = $env:WEBHOOK_BASE_URL
Push-Location $script:WebhookRoot
try {
    $env:WEBHOOK_DEMO_PASSWORD = $webhookValues['WEBHOOK_DEMO_PASSWORD']
    $env:WEBHOOK_BASE_URL = 'http://127.0.0.1:8097'
    & node (Join-Path $PSScriptRoot 'recovery-probe.mjs')
    if ($LASTEXITCODE -ne 0) { throw 'Recovery probe gagal. Periksa keluaran di atas.' }
} finally {
    $env:WEBHOOK_DEMO_PASSWORD = $webhookPreviousPassword
    $env:WEBHOOK_BASE_URL = $webhookPreviousBase
    Pop-Location
}
