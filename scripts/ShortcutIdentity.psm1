#Requires -Version 5.1

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'

if (-not ('CodexSubscriptionRouter.Windows.ShortcutIdentity' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

namespace CodexSubscriptionRouter.Windows
{
    public static class ShortcutIdentity
    {
        [StructLayout(LayoutKind.Sequential, Pack = 4)]
        private struct PropertyKey
        {
            internal Guid FormatId;
            internal uint PropertyId;
        }

        [StructLayout(LayoutKind.Explicit, Size = 24)]
        private struct PropVariant
        {
            [FieldOffset(0)] internal ushort VariantType;
            [FieldOffset(8)] internal IntPtr PointerValue;
        }

        [ComImport]
        [Guid("00021401-0000-0000-C000-000000000046")]
        private class ShellLink
        {
        }

        [ComImport]
        [Guid("0000010B-0000-0000-C000-000000000046")]
        [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
        private interface IPersistFile
        {
            [PreserveSig] int GetClassID(out Guid classId);
            [PreserveSig] int IsDirty();
            [PreserveSig] int Load([MarshalAs(UnmanagedType.LPWStr)] string fileName, uint mode);
            [PreserveSig] int Save([MarshalAs(UnmanagedType.LPWStr)] string fileName, [MarshalAs(UnmanagedType.Bool)] bool remember);
            [PreserveSig] int SaveCompleted([MarshalAs(UnmanagedType.LPWStr)] string fileName);
            [PreserveSig] int GetCurFile(out IntPtr fileName);
        }

        [ComImport]
        [Guid("886D8EEB-8CF2-4446-8D02-CDBA1DBDCF99")]
        [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
        private interface IPropertyStore
        {
            [PreserveSig] int GetCount(out uint count);
            [PreserveSig] int GetAt(uint index, out PropertyKey key);
            [PreserveSig] int GetValue(ref PropertyKey key, out PropVariant value);
            [PreserveSig] int SetValue(ref PropertyKey key, ref PropVariant value);
            [PreserveSig] int Commit();
        }

        [DllImport("propsys.dll", CharSet = CharSet.Unicode)]
        private static extern int PropVariantToStringAlloc(ref PropVariant variant, out IntPtr value);

        [DllImport("ole32.dll")]
        private static extern int PropVariantClear(ref PropVariant variant);

        [DllImport("ole32.dll")]
        private static extern void CoTaskMemFree(IntPtr value);

        private static readonly PropertyKey AppUserModelIdKey = new PropertyKey
        {
            FormatId = new Guid("9F4C2855-9F79-4B39-A8D0-E1D42DE1D5F3"),
            PropertyId = 5
        };

        public static void SetAppUserModelId(string shortcutPath, string appUserModelId)
        {
            object link = new ShellLink();
            try
            {
                IPersistFile persistFile = (IPersistFile)link;
                ThrowIfFailed(persistFile.Load(shortcutPath, 2), "IPersistFile.Load");
                IPropertyStore propertyStore = (IPropertyStore)link;
                PropVariant value = new PropVariant
                {
                    VariantType = 31,
                    PointerValue = Marshal.StringToCoTaskMemUni(appUserModelId)
                };
                try
                {
                    PropertyKey key = AppUserModelIdKey;
                    ThrowIfFailed(propertyStore.SetValue(ref key, ref value), "IPropertyStore.SetValue");
                    ThrowIfFailed(propertyStore.Commit(), "IPropertyStore.Commit");
                    ThrowIfFailed(persistFile.Save(shortcutPath, true), "IPersistFile.Save");
                }
                finally
                {
                    PropVariantClear(ref value);
                }
            }
            finally
            {
                Marshal.FinalReleaseComObject(link);
            }
        }

        public static string GetAppUserModelId(string shortcutPath)
        {
            object link = new ShellLink();
            try
            {
                IPersistFile persistFile = (IPersistFile)link;
                ThrowIfFailed(persistFile.Load(shortcutPath, 0), "IPersistFile.Load");
                IPropertyStore propertyStore = (IPropertyStore)link;
                PropertyKey key = AppUserModelIdKey;
                PropVariant value;
                ThrowIfFailed(propertyStore.GetValue(ref key, out value), "IPropertyStore.GetValue");
                try
                {
                    if (value.VariantType == 0)
                    {
                        return String.Empty;
                    }
                    IntPtr text;
                    ThrowIfFailed(PropVariantToStringAlloc(ref value, out text), "PropVariantToStringAlloc");
                    try
                    {
                        return Marshal.PtrToStringUni(text) ?? String.Empty;
                    }
                    finally
                    {
                        CoTaskMemFree(text);
                    }
                }
                finally
                {
                    PropVariantClear(ref value);
                }
            }
            finally
            {
                Marshal.FinalReleaseComObject(link);
            }
        }

        private static void ThrowIfFailed(int result, string operation)
        {
            if (result < 0)
            {
                throw new COMException(operation + " failed.", result);
            }
        }
    }
}
'@
}

function Set-CsrShortcutAppUserModelId {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][ValidateLength(1, 128)][string]$AppUserModelId
    )

    $resolved = (Get-Item -LiteralPath $Path -Force -ErrorAction Stop).FullName
    if (-not [IO.Path]::GetExtension($resolved).Equals('.lnk', [StringComparison]::OrdinalIgnoreCase)) {
        throw "Shortcut identity can only be assigned to a .lnk file: $resolved"
    }
    [CodexSubscriptionRouter.Windows.ShortcutIdentity]::SetAppUserModelId($resolved, $AppUserModelId)
}

function Get-CsrShortcutAppUserModelId {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)

    $resolved = (Get-Item -LiteralPath $Path -Force -ErrorAction Stop).FullName
    if (-not [IO.Path]::GetExtension($resolved).Equals('.lnk', [StringComparison]::OrdinalIgnoreCase)) {
        throw "Shortcut identity can only be read from a .lnk file: $resolved"
    }
    return [CodexSubscriptionRouter.Windows.ShortcutIdentity]::GetAppUserModelId($resolved)
}

Export-ModuleMember -Function @('Set-CsrShortcutAppUserModelId', 'Get-CsrShortcutAppUserModelId')
