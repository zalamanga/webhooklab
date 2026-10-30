[CmdletBinding()]
param()
. (Join-Path $PSScriptRoot 'Common.ps1')
$webhookEnvFile = Join-Path $script:WebhookRoot '.env'
$webhookUtf8 = New-Object System.Text.UTF8Encoding($false)
function New-WebhookSecret {
    $webhookBytes = New-Object byte[] 32
    $webhookRandom = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $webhookRandom.GetBytes($webhookBytes) } finally { $webhookRandom.Dispose() }
    return ([BitConverter]::ToString($webhookBytes)).Replace('-','').ToLowerInvariant()
}
if (-not (Test-Path -LiteralPath $webhookEnvFile)) {
    $webhookLines = @(
        ('WEBHOOK_DB_PASSWORD=' + (New-WebhookSecret))
        ('WEBHOOK_DEMO_PASSWORD=' + (New-WebhookSecret))
        ('WEBHOOK_SIGNING_SECRET=' + (New-WebhookSecret))
        ('WEBHOOK_TEST_DB_PASSWORD=' + (New-WebhookSecret))
        'WEBHOOK_APP_ORIGIN=http://127.0.0.1:8097'
    )
    [System.IO.File]::WriteAllLines($webhookEnvFile, $webhookLines, $webhookUtf8)
}
$webhookValues = Get-WebhookEnvironment
$webhookPrivate = Join-Path $script:WebhookRoot '.local'
New-Item -ItemType Directory -Path $webhookPrivate -Force | Out-Null
$webhookCredentials = @(
    'Akun lokal WebhookLab. File privat ini diabaikan Git.'
    'Alamat: http://127.0.0.1:8097'
    'Email: operator@webhooklab.test'
    ('Sandi: ' + $webhookValues['WEBHOOK_DEMO_PASSWORD'])
    ''
    'Sandi dipakai ketika tabel akun masih kosong; Init tidak mereset akun yang sudah tersimpan.'
    'Tidak ada pembayaran, email, atau pengiriman ke endpoint publik.'
)
[System.IO.File]::WriteAllLines((Join-Path $webhookPrivate 'LOCAL-CREDENTIALS.txt'), $webhookCredentials, $webhookUtf8)
Write-Output 'Konfigurasi siap. Baca akun pada .local/LOCAL-CREDENTIALS.txt.'
