!include LogicLib.nsh
!include WordFunc.nsh
; If the prerequisite needs a reboot, default the finish page to restarting later.
!define MUI_FINISHPAGE_REBOOTLATER_DEFAULT
!include "${__FILEDIR__}\..\binaries\vc_redist.x64.nsh"
!define ASTRLINK_VC_RUNTIME_INSTALLER "${__FILEDIR__}\..\binaries\vc_redist.x64.exe"
!define ASTRLINK_VC_RUNTIME_KEY "SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64"

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

!macro NSIS_HOOK_PREINSTALL
  Push $0
  Push $1
  Call AstrLinkHasVcRuntime
  Pop $0
  ${If} $0 != 1
    InitPluginsDir
    File /oname=$PLUGINSDIR\vc_redist.x64.exe "${ASTRLINK_VC_RUNTIME_INSTALLER}"
    DetailPrint "Installing Microsoft Visual C++ x64 runtime ${ASTRLINK_VC_RUNTIME_VERSION}..."
    ClearErrors
    ; Microsoft's bootstrapper requests elevation when needed. Keep AstrLink's
    ; current-user install mode; never reboot the machine from the prerequisite.
    ExecWait '"$PLUGINSDIR\vc_redist.x64.exe" /install /quiet /norestart' $0
    ${If} ${Errors}
      MessageBox MB_OK|MB_ICONSTOP "Unable to start Microsoft Visual C++ x64 setup. Administrator approval is required to install the model runtime." /SD IDOK
      SetErrorLevel 1603
      Abort
    ${EndIf}
    ${If} $0 == 3010
    ${OrIf} $0 == 1641
      SetRebootFlag true
    ${ElseIf} $0 != 0
    ${AndIf} $0 != 1638
      MessageBox MB_OK|MB_ICONSTOP "Microsoft Visual C++ x64 setup failed (exit code $0). AstrLink cannot install without its model runtime. See dd_vcredist_amd64*.log in your temporary folder, then retry installation." /SD IDOK
      SetErrorLevel $0
      Abort
    ${EndIf}
    ; 1638 can mean that another installer installed a newer runtime meanwhile.
    ; Do not treat it (or even exit code 0) as success without checking the result.
    Call AstrLinkHasVcRuntime
    Pop $1
    ${If} $1 != 1
      MessageBox MB_OK|MB_ICONSTOP "Microsoft Visual C++ x64 runtime ${ASTRLINK_VC_RUNTIME_VERSION} or newer is still unavailable. Restart Windows if requested, then retry AstrLink setup." /SD IDOK
      SetErrorLevel 1603
      Abort
    ${EndIf}
    Delete "$PLUGINSDIR\vc_redist.x64.exe"
  ${EndIf}
  Pop $1
  Pop $0
!macroend

!macro NSIS_HOOK_POSTINSTALL
  ; Make reboot requirements visible to unattended deployment tools as well.
  ${If} ${RebootFlag}
    SetErrorLevel 3010
  ${EndIf}
!macroend

; The VC runtime is shared with other applications. Never uninstall it here.
