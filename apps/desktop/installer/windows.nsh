; AstrLink's part of the Windows installer `mygo build` makes
; (windows.installerInclude in mygo.config.ts). MyGo includes this file after
; MUI2.nsh and the languages, runs customInit in .onInit and customInstall at
; the end of the install, silent installs (/S) included.
!include LogicLib.nsh
!include WordFunc.nsh
!include FileFunc.nsh
; Staged by scripts/stage-windows-vc-runtime.mjs (bun run sidecar:build).
!include "${__FILEDIR__}\..\build\staging\vc_redist.x64.nsh"
!define ASTRLINK_VC_RUNTIME_INSTALLER "${__FILEDIR__}\..\build\staging\vc_redist.x64.exe"
!define ASTRLINK_VC_RUNTIME_KEY "SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64"
; Earlier releases installed per user into %LOCALAPPDATA%\AstrLink and listed
; themselves under the product name; this installer replaces such an install.
!define ASTRLINK_LEGACY_UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\AstrLink"
!define ASTRLINK_RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"
!define ASTRLINK_APPROVED_KEY "Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run"

; Return 1 only when the machine has a sufficiently new x64 runtime.
; An x86 runtime does not satisfy the x64 workers' dependencies.
Function AstrLinkHasVcRuntime
  Push $0
  Push $1
  Push $2
  Push $3
  Push $4
  SetRegView 64
  ClearErrors
  ReadRegDWORD $0 HKLM "${ASTRLINK_VC_RUNTIME_KEY}" "Installed"
  ${If} $0 == 1
    ReadRegDWORD $1 HKLM "${ASTRLINK_VC_RUNTIME_KEY}" "Major"
    ReadRegDWORD $2 HKLM "${ASTRLINK_VC_RUNTIME_KEY}" "Minor"
    ReadRegDWORD $3 HKLM "${ASTRLINK_VC_RUNTIME_KEY}" "Bld"
    ReadRegDWORD $4 HKLM "${ASTRLINK_VC_RUNTIME_KEY}" "Rbld"
    ${IfNot} ${Errors}
      ${VersionCompare} "$1.$2.$3.$4" "${ASTRLINK_VC_RUNTIME_VERSION}" $0
      ${If} $0 != 2
        StrCpy $0 1
      ${Else}
        StrCpy $0 0
      ${EndIf}
    ${Else}
      StrCpy $0 0
    ${EndIf}
  ${Else}
    StrCpy $0 0
  ${EndIf}
  SetRegView lastused
  Pop $4
  Pop $3
  Pop $2
  Pop $1
  Exch $0
FunctionEnd

; Installs the Visual C++ runtime the workers need, unless the machine has it.
!macro AstrLinkInstallVcRuntime
  Push $0
  Push $1
  Call AstrLinkHasVcRuntime
  Pop $0
  ${If} $0 != 1
    InitPluginsDir
    File /oname=$PLUGINSDIR\vc_redist.x64.exe "${ASTRLINK_VC_RUNTIME_INSTALLER}"
    ClearErrors
    ; Microsoft's bootstrapper requests elevation when needed. Keep AstrLink's
    ; current-user install mode; never reboot the machine from the prerequisite.
    ExecWait '"$PLUGINSDIR\vc_redist.x64.exe" /install /quiet /norestart' $0
    ${If} ${Errors}
      MessageBox MB_OK|MB_ICONSTOP "Unable to start Microsoft Visual C++ x64 setup. Administrator approval is required to install the model runtime." /SD IDOK
      SetErrorLevel 1603
      Quit
    ${EndIf}
    ${If} $0 == 3010
    ${OrIf} $0 == 1641
      SetRebootFlag true
    ${ElseIf} $0 != 0
    ${AndIf} $0 != 1638
      MessageBox MB_OK|MB_ICONSTOP "Microsoft Visual C++ x64 setup failed (exit code $0). AstrLink cannot install without its model runtime. See dd_vcredist_amd64*.log in your temporary folder, then retry installation." /SD IDOK
      SetErrorLevel $0
      Quit
    ${EndIf}
    ; 1638 can mean that another installer installed a newer runtime meanwhile.
    ; Do not treat it (or even exit code 0) as success without checking the result.
    Call AstrLinkHasVcRuntime
    Pop $1
    ${If} $1 != 1
      MessageBox MB_OK|MB_ICONSTOP "Microsoft Visual C++ x64 runtime ${ASTRLINK_VC_RUNTIME_VERSION} or newer is still unavailable. Restart Windows if requested, then retry AstrLink setup." /SD IDOK
      SetErrorLevel 1603
      Quit
    ${EndIf}
    Delete "$PLUGINSDIR\vc_redist.x64.exe"
  ${EndIf}
  Pop $1
  Pop $0
!macroend

; Removes an install of an earlier release with its own uninstaller, run
; silently and waited for, so one AstrLink stays installed. The uninstaller
; keeps the user's data.
!macro AstrLinkRemoveLegacyInstall
  Push $0
  Push $1
  Push $2
  ClearErrors
  ReadRegStr $0 HKCU "${ASTRLINK_LEGACY_UNINSTALL_KEY}" "UninstallString"
  ${IfNot} ${Errors}
  ${AndIf} $0 != ""
    ; "C:\...\uninstall.exe", quoted.
    StrCpy $1 $0 1
    ${If} $1 == '"'
      StrCpy $0 $0 "" 1
      ${WordFind} "$0" '"' "+1" $0
    ${EndIf}
    ${GetParent} "$0" $1
    ${If} ${FileExists} "$0"
    ${AndIf} $1 != ""
    ${AndIf} $1 != "$INSTDIR"
      ; _?= runs the uninstaller in place, so ExecWait waits for it.
      ExecWait '"$0" /S _?=$1' $2
      ${If} $2 == 0
        Delete "$0"
        RMDir "$1"
      ${EndIf}
    ${EndIf}
  ${EndIf}
  ; The earlier releases' login entry, named after the product and starting
  ; the app with --autostart; the app's own entry has another name.
  ClearErrors
  ReadRegStr $0 HKCU "${ASTRLINK_RUN_KEY}" "AstrLink"
  ${IfNot} ${Errors}
    ClearErrors
    ${WordFind} "$0" "--autostart" "E#" $1
    ${IfNot} ${Errors}
      DeleteRegValue HKCU "${ASTRLINK_RUN_KEY}" "AstrLink"
      DeleteRegValue HKCU "${ASTRLINK_APPROVED_KEY}" "AstrLink"
    ${EndIf}
  ${EndIf}
  ClearErrors
  Pop $2
  Pop $1
  Pop $0
!macroend

!macro customInit
  !insertmacro AstrLinkInstallVcRuntime
  !insertmacro AstrLinkRemoveLegacyInstall
!macroend

!macro customInstall
  ; Make reboot requirements visible to unattended deployment tools as well.
  ${If} ${RebootFlag}
    SetErrorLevel 3010
  ${EndIf}
!macroend

; The VC runtime is shared with other applications. Never uninstall it here.
