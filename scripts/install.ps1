& {
    $ErrorActionPreference = 'Stop'
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (!$arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    if ($env:OS -ne 'Windows_NT' -or $arch -ne 'AMD64') {
        throw 'This installer requires Windows x64.'
    }

    $base = '@RELEASE_BASE@'
    $asset = '@SETUP@'
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('mygo-install-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    $setup = Join-Path $tmp $asset
    $checksums = Join-Path $tmp 'SHA256SUMS'
    try {
        Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $checksums
        $pattern = '^([a-fA-F0-9]{64}) [ *]' + [regex]::Escape($asset) + '$'
        $checksumRows = @(Select-String -LiteralPath $checksums -Pattern $pattern)
        if ($checksumRows.Count -ne 1) { throw 'Installer checksum is missing or ambiguous.' }
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $setup
        if ((Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash -ne $checksumRows[0].Matches[0].Groups[1].Value) {
            throw 'Installer checksum did not match.'
        }
        $process = Start-Process -FilePath $setup -Wait -PassThru
        if ($process.ExitCode -ne 0) { throw "Installer exited with code $($process.ExitCode)." }
    } finally {
        foreach ($file in $setup, $checksums) {
            if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file -Force }
        }
        [IO.Directory]::Delete($tmp)
    }
}
