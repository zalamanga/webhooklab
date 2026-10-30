[CmdletBinding()]
param([switch]$NoBuild)
. (Join-Path $PSScriptRoot 'Common.ps1')
& (Join-Path $PSScriptRoot 'Init.ps1')
$webhookValues = Get-WebhookEnvironment
if ($webhookValues['WEBHOOK_APP_ORIGIN'] -ne 'http://127.0.0.1:8097') { throw 'Compose ini memakai http://127.0.0.1:8097. Ubah origin dan port bersama bila memodifikasi deployment.' }
Push-Location $script:WebhookRoot
try {
    if (-not $NoBuild) {
        Invoke-WebhookDocker -Arguments @('compose','build','api')
        Invoke-WebhookDocker -Arguments @('compose','build','web')
    }
    Invoke-WebhookDocker -Arguments @('compose','up','-d','--no-build','--wait','--wait-timeout','180')
    Write-Output 'WebhookLab: http://127.0.0.1:8097'
    Write-Output 'Akun: .local/LOCAL-CREDENTIALS.txt'
} finally { Pop-Location }
