$ErrorActionPreference = 'Stop'
$script:WebhookRoot = Split-Path -Parent $PSScriptRoot

function Invoke-WebhookDocker {
    param([Parameter(Mandatory=$true)][string[]]$Arguments)
    $webhookPriorPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        & docker @Arguments
        if ($LASTEXITCODE -ne 0) { throw 'Perintah Docker gagal. Periksa keluaran di atas.' }
    } finally { $ErrorActionPreference = $webhookPriorPreference }
}

function Get-WebhookEnvironment {
    $webhookValues = @{}
    $webhookEnvFile = Join-Path $script:WebhookRoot '.env'
    if (-not (Test-Path -LiteralPath $webhookEnvFile)) { throw 'Jalankan scripts/Init.ps1 terlebih dahulu.' }
    foreach ($webhookLine in [System.IO.File]::ReadAllLines($webhookEnvFile)) {
        if ($webhookLine -match '^([A-Z][A-Z0-9_]*)=(.*)$') { $webhookValues[$matches[1]] = $matches[2] }
    }
    foreach ($webhookKey in @('WEBHOOK_DB_PASSWORD','WEBHOOK_DEMO_PASSWORD','WEBHOOK_SIGNING_SECRET','WEBHOOK_TEST_DB_PASSWORD')) {
        if (-not $webhookValues[$webhookKey] -or $webhookValues[$webhookKey].Length -lt 32) { throw 'Konfigurasi privat belum lengkap; periksa .env lokal.' }
    }
    return $webhookValues
}
