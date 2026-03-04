@echo off
setlocal enabledelayedexpansion

:: Determine plugin root: prefer env var set by Claude Code plugin system,
:: fall back to deriving from script location.
if defined CLAUDE_PLUGIN_ROOT (
  set "PLUGIN_ROOT=%CLAUDE_PLUGIN_ROOT%"
) else (
  set "PLUGIN_ROOT=%~dp0.."
)

:: Architecture detection
set "ARCH=amd64"
if "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "ARCH=arm64"

:: Environment defaults
if not defined LUMEN_BACKEND set "LUMEN_BACKEND=ollama"
if not defined LUMEN_EMBED_MODEL set "LUMEN_EMBED_MODEL=ordis/jina-embeddings-v2-base-code"

:: Binary path
set "BINARY=%PLUGIN_ROOT%\bin\lumen-windows-%ARCH%.exe"

:: Download on first run if binary is missing
if not exist "%BINARY%" (
  set "REPO=ory/lumen"

  :: Always use the version pinned in the manifest — keeps plugin and binary in sync
  set "MANIFEST=%PLUGIN_ROOT%\.release-please-manifest.json"
  if not exist "!MANIFEST!" (
    echo Error: .release-please-manifest.json not found in %PLUGIN_ROOT% >&2
    exit /b 1
  )
  for /f "tokens=*" %%i in ('findstr /r "\"[.]\"" "!MANIFEST!"') do (
    for /f "tokens=2 delims=:" %%j in ("%%i") do (
      set "VERSION=v%%~j"
      set "VERSION=!VERSION: =!"
      set "VERSION=!VERSION:,=!"
      set "VERSION=!VERSION:"=!"
    )
  )

  if "!VERSION!"=="" (
    echo Error: could not read version from !MANIFEST! >&2
    exit /b 1
  )

  set "ASSET=lumen-!VERSION:~1!-windows-!ARCH!.exe"
  set "URL=https://github.com/!REPO!/releases/download/!VERSION!/!ASSET!"

  echo Downloading lumen !VERSION! for windows/!ARCH!... >&2
  if not exist "%PLUGIN_ROOT%\bin" mkdir "%PLUGIN_ROOT%\bin"

  curl -sfL "!URL!" -o "%BINARY%"

  echo Installed lumen to %BINARY% >&2
)

"%BINARY%" %*
