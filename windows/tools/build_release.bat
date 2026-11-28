@echo off
setlocal EnableExtensions

rem Package the Windows tools from this checkout.
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

echo [1/5] Verifying the embedded Rust FULL image...
go test -C "%ROOT%\tools\inst40hx" -count=1 -run "^TestEmbeddedRustEfiIsFull$" .
if errorlevel 1 exit /b 1

echo [2/5] Building Windows tools...
go build -C "%ROOT%\tools\inst40hx" -a -trimpath -ldflags="-H=windowsgui -s -w" -o "%OUT%\40HXInstaller.exe" .
if errorlevel 1 goto :fail
go build -C "%ROOT%\tools\uninstall40x" -a -trimpath -ldflags="-H=windowsgui -s -w" -o "%OUT%\40HXUninstaller.exe" .
if errorlevel 1 goto :fail
go build -C "%ROOT%\tools\check40x" -a -trimpath -ldflags="-H=windowsgui -s -w" -o "%OUT%\40HXCheck.exe" .
if errorlevel 1 goto :fail

echo [3/5] Copying package files...
copy /y "%ROOT%\tools\inst40hx\embed\40HXUNLK.EFI" "%OUT%\files\40HXUNLK.EFI" >nul || goto :fail
copy /y "%ROOT%\tools\inst40hx\embed\ThrottleStop.sys" "%OUT%\gen2\drivers\ThrottleStop.sys" >nul || goto :fail
copy /y "%ROOT%\tools\inst40hx\embed\WinRing0x64.sys" "%OUT%\gen2\drivers\WinRing0x64.sys" >nul || goto :fail
copy /y "%ROOT%\tools\inst40hx\embed\WinRing0x64.sys" "%OUT%\files\WinRing0x64.sys" >nul || goto :fail
copy /y "%ROOT%\40HX_Gen2_Windows\source\WinRing0-master\LICENSE" "%OUT%\WinRing0-LICENSE.txt" >nul || goto :fail
copy /y "%ROOT%\FORK_CHANGES.md" "%OUT%\FORK_CHANGES.md" >nul || goto :fail
copy /y "%ROOT%\release-files\README.md" "%OUT%\README.md" >nul || goto :fail
copy /y "%ROOT%\release-files\EFI*.md" "%OUT%\" >nul || goto :fail
copy /y "%ROOT%\release-files\make_usb_efi.bat" "%OUT%\make_usb_efi.bat" >nul || goto :fail
copy /y "%ROOT%\release-files\copy_efi_verified.ps1" "%OUT%\copy_efi_verified.ps1" >nul || goto :fail
copy /y "%ROOT%\tools\verify_source.ps1" "%OUT%\verify_source.ps1" >nul || goto :fail

echo [4/5] Writing build metadata...
>"%OUT%\BUILD_INFO.txt" echo EFI implementation: Rust FULL
>>"%OUT%\BUILD_INFO.txt" echo EFI default mode: automatic Windows chainload
>>"%OUT%\BUILD_INFO.txt" echo Compute payload: FULL compute unlock, 960 KiB Booter heap
>>"%OUT%\BUILD_INFO.txt" echo Boot-manager handoff: add the --return-to-bootloader load option to the 40HX boot entry

echo [5/5] Writing SHA256SUMS.txt...
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0write_package_manifest.ps1" -Directory "%OUT%"
if errorlevel 1 exit /b 1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%OUT%\verify_source.ps1"
if errorlevel 1 exit /b 1

echo.
echo Build complete: %OUT%
exit /b 0

:fail
echo [ERROR] Build or packaging failed.
exit /b 1
