@echo off
setlocal EnableExtensions

rem Build and package the source-based Cyrid fork without touching release/.
set "ROOT=%~dp0.."
set "OUT=%ROOT%\release-fork"
set "CGO_ENABLED=0"
set "GOOS=windows"
set "GOARCH=amd64"

where go >nul 2>&1
if errorlevel 1 (
    echo [ERROR] Go was not found in PATH.
    exit /b 1
)

if not exist "%OUT%" mkdir "%OUT%"
if not exist "%OUT%\gen2\drivers" mkdir "%OUT%\gen2\drivers"
if not exist "%OUT%\files" mkdir "%OUT%\files"

echo [1/5] Trying to rebuild the EFI payload...
where bash >nul 2>&1
if not errorlevel 1 (
    pushd "%ROOT%\tools\unlock40x"
    bash build_v70.sh
    if not errorlevel 1 if exist unlock40x_v70.efi copy /y unlock40x_v70.efi "%ROOT%\tools\inst40hx\embed\40HXUNLK.EFI" >nul
    popd
) else (
    echo [INFO] bash was not found; using the checked-in embedded EFI.
)

echo [2/5] Building Windows tools...
pushd "%ROOT%\tools\inst40hx"
go build -a -trimpath -ldflags="-H=windowsgui -s -w" -o "%OUT%\40HXInstaller.exe" .
if errorlevel 1 goto :fail
popd
pushd "%ROOT%\tools\uninstall40x"
go build -a -trimpath -ldflags="-H=windowsgui -s -w" -o "%OUT%\40HXUninstaller.exe" .
if errorlevel 1 goto :fail
popd
pushd "%ROOT%\tools\check40x"
go build -a -trimpath -ldflags="-H=windowsgui -s -w" -o "%OUT%\40HXCheck.exe" .
if errorlevel 1 goto :fail
popd

echo [3/5] Copying package files...
copy /y "%ROOT%\tools\inst40hx\embed\40HXUNLK.EFI" "%OUT%\files\40HXUNLK.EFI" >nul || goto :fail
copy /y "%ROOT%\tools\inst40hx\embed\ThrottleStop.sys" "%OUT%\gen2\drivers\ThrottleStop.sys" >nul || goto :fail
copy /y "%ROOT%\tools\inst40hx\embed\WinRing0x64.sys" "%OUT%\gen2\drivers\WinRing0x64.sys" >nul || goto :fail
copy /y "%ROOT%\tools\inst40hx\embed\WinRing0x64.sys" "%OUT%\files\WinRing0x64.sys" >nul || goto :fail
copy /y "%ROOT%\FORK_CHANGES.md" "%OUT%\FORK_CHANGES.md" >nul || goto :fail
copy /y "%ROOT%\release-files\README.md" "%OUT%\README.md" >nul || goto :fail
copy /y "%ROOT%\release-files\EFI应急修复指南.md" "%OUT%\EFI应急修复指南.md" >nul || goto :fail
copy /y "%ROOT%\release-files\AI辅助安装提示词.txt" "%OUT%\AI辅助安装提示词.txt" >nul || goto :fail
copy /y "%ROOT%\release-files\make_usb_efi.bat" "%OUT%\make_usb_efi.bat" >nul || goto :fail
copy /y "%ROOT%\release-files\OpenCL.exe" "%OUT%\OpenCL.exe" >nul || goto :fail
copy /y "%ROOT%\tools\verify_source.ps1" "%OUT%\verify_source.ps1" >nul

echo [4/5] Writing build metadata...
>"%OUT%\BUILD_INFO.txt" echo Source tree: %ROOT%
>>"%OUT%\BUILD_INFO.txt" echo Built: %DATE% %TIME%
>>"%OUT%\BUILD_INFO.txt" echo EFI default mode: automatic Windows chainload
>>"%OUT%\BUILD_INFO.txt" echo Boot-manager handoff: add the --return-to-bootloader load option to the 40HX boot entry

echo [5/5] Writing SHA256SUMS.txt...
powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$out=[IO.Path]::GetFullPath('%OUT%'); Get-ChildItem -LiteralPath $out -File -Recurse | Where-Object { $_.Name -ne 'SHA256SUMS.txt' } | Sort-Object FullName | ForEach-Object { $h=(Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant(); $r=$_.FullName.Substring($out.Length+1); '{0}  {1}' -f $h,$r } | Set-Content -LiteralPath (Join-Path $out 'SHA256SUMS.txt') -Encoding ascii"
if errorlevel 1 goto :fail

echo.
echo Build complete: %OUT%
exit /b 0

:fail
echo [ERROR] Build or packaging failed.
popd >nul 2>nul
exit /b 1
