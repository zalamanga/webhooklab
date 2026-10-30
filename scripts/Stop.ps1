[CmdletBinding()]
param()
. (Join-Path $PSScriptRoot 'Common.ps1')
Push-Location $script:WebhookRoot
try { Invoke-WebhookDocker -Arguments @('compose','down') } finally { Pop-Location }
Write-Output 'WebhookLab dihentikan. Volume PostgreSQL tetap tersimpan.'
