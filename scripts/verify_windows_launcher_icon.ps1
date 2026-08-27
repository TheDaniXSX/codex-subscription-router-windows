#Requires -Version 5.1

<#
.SYNOPSIS
Verifies that the independent Windows launcher contains an icon resource.

.PARAMETER LauncherPath
Path to the built x64 launcher executable.

.PARAMETER SourceIconPath
Optional path to the multi-resolution ICO used as the build input. When
provided, its directory table is validated before inspecting the executable.
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$LauncherPath,

    [string]$SourceIconPath
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'

if ($env:OS -ne 'Windows_NT') {
    throw 'Launcher icon verification supports Windows only.'
}

$launcher = [IO.Path]::GetFullPath($LauncherPath)
if (-not (Test-Path -LiteralPath $launcher -PathType Leaf) -or
    -not [IO.Path]::GetExtension($launcher).Equals('.exe', [StringComparison]::OrdinalIgnoreCase)) {
    throw "LauncherPath must identify an existing .exe: $launcher"
}

$iconSizes = @()
if (-not [string]::IsNullOrWhiteSpace($SourceIconPath)) {
    $sourceIcon = [IO.Path]::GetFullPath($SourceIconPath)
    if (-not (Test-Path -LiteralPath $sourceIcon -PathType Leaf) -or
        -not [IO.Path]::GetExtension($sourceIcon).Equals('.ico', [StringComparison]::OrdinalIgnoreCase)) {
        throw "SourceIconPath must identify an existing .ico: $sourceIcon"
    }

    $stream = [IO.File]::OpenRead($sourceIcon)
    $reader = New-Object IO.BinaryReader($stream)
    try {
        if ($reader.ReadUInt16() -ne 0 -or $reader.ReadUInt16() -ne 1) {
            throw "ICO header is invalid: $sourceIcon"
        }
        $count = [int]$reader.ReadUInt16()
        if ($count -lt 1) {
            throw "ICO contains no images: $sourceIcon"
        }
        for ($index = 0; $index -lt $count; $index++) {
            $widthByte = [int]$reader.ReadByte()
            $heightByte = [int]$reader.ReadByte()
            $width = if ($widthByte -eq 0) { 256 } else { $widthByte }
            $height = if ($heightByte -eq 0) { 256 } else { $heightByte }
            if ($width -ne $height) {
                throw "ICO entry $index is not square: ${width}x${height}"
            }
            $iconSizes += $width
            [void]$reader.ReadByte() # palette size
            [void]$reader.ReadByte() # reserved
            [void]$reader.ReadUInt16() # planes
            [void]$reader.ReadUInt16() # bits per pixel
            $bytesInResource = [UInt32]$reader.ReadUInt32()
            $imageOffset = [UInt32]$reader.ReadUInt32()
            if ($bytesInResource -eq 0 -or ([UInt64]$imageOffset + $bytesInResource) -gt [UInt64]$stream.Length) {
                throw "ICO entry $index points outside the file."
            }
        }
    }
    finally {
        $reader.Dispose()
        $stream.Dispose()
    }

    $expectedSizes = @(16, 20, 24, 32, 40, 48, 64, 128, 256)
    $missingSizes = @($expectedSizes | Where-Object { $iconSizes -notcontains $_ })
    if ($missingSizes.Count -gt 0) {
        throw "ICO is missing required sizes: $($missingSizes -join ', ')"
    }
}

if (-not ('CodexRouterIconNativeMethods' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class CodexRouterIconNativeMethods
{
    [DllImport("shell32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern uint ExtractIconEx(
        string fileName,
        int iconIndex,
        out IntPtr largeIcon,
        out IntPtr smallIcon,
        uint iconCount);

    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool DestroyIcon(IntPtr icon);
}
'@
}

$largeIcon = [IntPtr]::Zero
$smallIcon = [IntPtr]::Zero
try {
    $count = [CodexRouterIconNativeMethods]::ExtractIconEx(
        $launcher,
        0,
        [ref]$largeIcon,
        [ref]$smallIcon,
        1
    )
    if ($count -lt 1 -or $largeIcon -eq [IntPtr]::Zero -or $smallIcon -eq [IntPtr]::Zero) {
        throw "Launcher contains no extractable large/small icon resource: $launcher"
    }
}
finally {
    if ($largeIcon -ne [IntPtr]::Zero) {
        [void][CodexRouterIconNativeMethods]::DestroyIcon($largeIcon)
    }
    if ($smallIcon -ne [IntPtr]::Zero) {
        [void][CodexRouterIconNativeMethods]::DestroyIcon($smallIcon)
    }
}

[PSCustomObject]@{
    Launcher = $launcher
    EmbeddedIconGroups = [int]$count
    SourceIconSizes = @($iconSizes | Sort-Object -Unique)
    Passed = $true
}
