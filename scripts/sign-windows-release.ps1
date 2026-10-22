# Authenticode-sign all PE executables in the validated portable desktop archive.
param([Parameter(Mandatory=$true)][string]$Version, [Parameter(Mandatory=$true)][string]$Output)
$ErrorActionPreference = 'Stop'
$archive = Join-Path $Output "pot-desktop_${Version}_windows_amd64.zip"
$digest = (Get-FileHash $archive -Algorithm SHA256).Hash.ToLowerInvariant()
$expected = "$digest  $([IO.Path]::GetFileName($archive))"
if ((Get-Content "$archive.sha256" -Raw).TrimEnd() -cne $expected) { throw 'Input checksum mismatch' }
$stage = Join-Path $env:RUNNER_TEMP "pot-windows-sign-$([guid]::NewGuid())"
New-Item -ItemType Directory -Path $stage | Out-Null
$certificatePath = Join-Path $stage 'certificate.pfx'
$certificate = $null
try {
  [IO.File]::WriteAllBytes($certificatePath, [Convert]::FromBase64String($env:WINDOWS_CERTIFICATE_PFX))
  $password = ConvertTo-SecureString $env:WINDOWS_CERTIFICATE_PASSWORD -AsPlainText -Force
  $certificate = Import-PfxCertificate -FilePath $certificatePath -CertStoreLocation Cert:\CurrentUser\My -Password $password
  if (-not $certificate.HasPrivateKey) { throw 'Signing certificate has no private key' }
  $signTool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" | Sort-Object FullName -Descending | Select-Object -First 1
  if (-not $signTool) { throw 'Windows SDK signtool is required' }
  $tree = Join-Path $stage 'package'
  Expand-Archive -LiteralPath $archive -DestinationPath $tree
  $binaries = @(Get-ChildItem $tree -Recurse -File | Where-Object { $_.Extension -in '.exe', '.dll', '.node' })
  if ($binaries.Count -eq 0) { throw 'Archive contains no Windows executables' }
  foreach ($binary in $binaries) {
    & $signTool.FullName sign /sha1 $certificate.Thumbprint /fd SHA256 /tr 'http://timestamp.digicert.com' /td SHA256 $binary.FullName
    if ($LASTEXITCODE -ne 0) { throw "Signing failed: $($binary.Name)" }
    & $signTool.FullName verify /pa /all /tw $binary.FullName
    if ($LASTEXITCODE -ne 0) { throw "Signature verification failed: $($binary.Name)" }
  }
  Remove-Item -LiteralPath $archive
  # .NET includes hidden package assets, which Compress-Archive omits.
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  [IO.Compression.ZipFile]::CreateFromDirectory($tree, $archive)
  $digest = (Get-FileHash $archive -Algorithm SHA256).Hash.ToLowerInvariant()
  [IO.File]::WriteAllText("$archive.sha256", "$digest  $([IO.Path]::GetFileName($archive))`n")
} finally {
  if ($certificate) { Remove-Item "Cert:\CurrentUser\My\$($certificate.Thumbprint)" -DeleteKey }
  Remove-Item -LiteralPath $stage -Recurse -Force
}
