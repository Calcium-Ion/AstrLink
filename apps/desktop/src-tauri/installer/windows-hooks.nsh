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

; ---------------------------------------------------------------------------
; Running AstrLink processes
;
; Replaces the template's CheckIfAppIsRunning. That check looks only for the
; main executable, by name, for any install and any user, and gives a killed
; process a fixed half second before files are overwritten. Core's helper
; processes are never checked. An update starts this installer while the app
; is still exiting and while Core's workers may still be shutting down, so
; File could find an executable or DirectML.dll still in use and stop with
; "Error opening file for writing".
;
; This check matches only the current user's processes whose image is one of
; this package's executables in $INSTDIR, waits for them to exit, and then
; waits until every file the installer replaces can be written. Processes of
; other users or of AstrLink copies in other directories are left alone. An
; image matches by file identity, so a junction, subst drive or short name in
; either path does not hide a running copy.
; ---------------------------------------------------------------------------

; PROCESSENTRY32W is read with its 32-bit layout below, and handles that may
; be INVALID_HANDLE_VALUE are returned as signed integers.
!if ${NSIS_PTR_SIZE} > 4
  !error "AstrLinkProcesses expects a 32-bit installer"
!endif

!macro ASTRLINK_PROCESS_FUNCTIONS UN
; Pops a process handle with PROCESS_QUERY_LIMITED_INFORMATION access and
; pushes 1 when that process runs as the same user as this installer.
Function ${UN}AstrLinkSameUser
  Exch $0
  Push $1
  Push $2
  Push $3
  Push $4
  Push $5
  Push $6
  StrCpy $5 0
  System::Alloc 1024
  Pop $1
  System::Alloc 1024
  Pop $2
  ; TOKEN_QUERY, TokenUser
  System::Call 'advapi32::OpenProcessToken(p r0, i 8, *p .r3) i .r4'
  ${If} $4 <> 0
    System::Call 'advapi32::GetTokenInformation(p r3, i 1, p r1, i 1024, *i .r6) i .r4'
    System::Call 'kernel32::CloseHandle(p r3)'
    ${If} $4 <> 0
      System::Call 'kernel32::GetCurrentProcess() p .r0'
      System::Call 'advapi32::OpenProcessToken(p r0, i 8, *p .r3) i .r4'
      ${If} $4 <> 0
        System::Call 'advapi32::GetTokenInformation(p r3, i 1, p r2, i 1024, *i .r6) i .r4'
        System::Call 'kernel32::CloseHandle(p r3)'
        ${If} $4 <> 0
          ; TOKEN_USER starts with the user's SID pointer.
          System::Call '*$1(p .r3)'
          System::Call '*$2(p .r4)'
          System::Call 'advapi32::EqualSid(p r3, p r4) i .r4'
          ${If} $4 <> 0
            StrCpy $5 1
          ${EndIf}
        ${EndIf}
      ${EndIf}
    ${EndIf}
  ${EndIf}
  System::Free $1
  System::Free $2
  StrCpy $0 $5
  Pop $6
  Pop $5
  Pop $4
  Pop $3
  Pop $2
  Pop $1
  Exch $0
FunctionEnd

; Pops a path and pushes the file's volume serial number and file index, or an
; empty string when the file cannot be opened or has no index.
Function ${UN}AstrLinkFileId
  Exch $0
  Push $1
  Push $2
  Push $3
  Push $4
  Push $5
  StrCpy $5 ""
  ; FILE_READ_ATTRIBUTES; shares read, write and delete, so a running image
  ; can be opened; OPEN_EXISTING.
  System::Call 'kernel32::CreateFileW(w r0, i 0x80, i 7, p 0, i 3, i 0, p 0) i .r1'
  ${If} $1 <> -1
    ; BY_HANDLE_FILE_INFORMATION, 13 DWORDs
    System::Call '*(i, i, i, i, i, i, i, i, i, i, i, i, i) p .r2'
    System::Call 'kernel32::GetFileInformationByHandle(p r1, p r2) i .r3'
    ${If} $3 <> 0
      System::Call '*$2(i, i, i, i, i, i, i, i .r3, i, i, i, i .r4, i .r0)'
      ${If} $4 <> 0
      ${OrIf} $0 <> 0
        StrCpy $5 "$3:$4:$0"
      ${EndIf}
    ${EndIf}
    System::Free $2
    System::Call 'kernel32::CloseHandle(p r1)'
  ${EndIf}
  StrCpy $0 $5
  Pop $5
  Pop $4
  Pop $3
  Pop $2
  Pop $1
  Exch $0
FunctionEnd

; Pops an action (0 counts, 1 ends the processes) and the main executable's
; file name. Pushes how many matching processes are still running: those of
; the current user whose image is the main executable or one of Core's helper
; executables in $INSTDIR.
Function ${UN}AstrLinkProcesses
  Exch $1
  Exch
  Exch $0
  Push $2
  Push $3
  Push $4
  Push $5
  Push $6
  Push $7
  Push $8
  Push $9
  Push $R0
  StrCpy $9 0
  ; TH32CS_SNAPPROCESS
  System::Call 'kernel32::CreateToolhelp32Snapshot(i 2, i 0) i .r2'
  ${If} $2 <> -1
    System::Call '*(i 556, i, i, i, i, i, i, i, i, &w260) p .r3'
    System::Call 'kernel32::Process32FirstW(p r2, p r3) i .r4'
    ${DoWhile} $4 <> 0
      System::Call '*$3(i, i, i .r5, i, i, i, i, i, i, &w260 .r6)'
      ${If} $6 == $0
      ${OrIf} $6 == "astrlink-core.exe"
      ${OrIf} $6 == "astrlink-privacy-worker.exe"
      ${OrIf} $6 == "astrlink-classifier-worker.exe"
      ${OrIf} $6 == "astrlink-cli.exe"
        ; PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE
        System::Call 'kernel32::OpenProcess(i 0x101000, i 0, i r5) p .r7'
        ${If} $7 <> 0
          System::Call 'kernel32::QueryFullProcessImageNameW(p r7, i 0, w .R0, *i ${NSIS_MAX_STRLEN}) i .r4'
          StrCpy $8 0
          ${If} $4 <> 0
            ${If} $R0 == "$INSTDIR\$6"
              StrCpy $8 1
            ${Else}
              Push "$INSTDIR\$6"
              Call ${UN}AstrLinkFileId
              Pop $4
              ${If} $4 != ""
                Push $R0
                Call ${UN}AstrLinkFileId
                Pop $R0
                ${If} $R0 == $4
                  StrCpy $8 1
                ${EndIf}
              ${EndIf}
            ${EndIf}
          ${EndIf}
          ${If} $8 = 1
            Push $7
            Call ${UN}AstrLinkSameUser
            Pop $4
            ${If} $4 = 1
              StrCpy $4 0
              ${If} $1 = 1
                ; PROCESS_TERMINATE
                System::Call 'kernel32::OpenProcess(i 1, i 0, i r5) p .r4'
                ${If} $4 <> 0
                  System::Call 'kernel32::TerminateProcess(p r4, i 1)'
                  System::Call 'kernel32::CloseHandle(p r4)'
                  ; Termination is asynchronous; the image stays in use
                  ; until the process has exited.
                  StrCpy $4 10000
                ${EndIf}
              ${EndIf}
              ; Anything but WAIT_OBJECT_0 means it is still running.
              System::Call 'kernel32::WaitForSingleObject(p r7, i r4) i .r4'
              ${If} $4 <> 0
                IntOp $9 $9 + 1
              ${EndIf}
            ${EndIf}
          ${EndIf}
          System::Call 'kernel32::CloseHandle(p r7)'
        ${EndIf}
      ${EndIf}
      System::Call 'kernel32::Process32NextW(p r2, p r3) i .r4'
    ${Loop}
    System::Free $3
    System::Call 'kernel32::CloseHandle(p r2)'
  ${EndIf}
  StrCpy $0 $9
  Pop $R0
  Pop $9
  Pop $8
  Pop $7
  Pop $6
  Pop $5
  Pop $4
  Pop $3
  Pop $2
  Exch
  Pop $1
  Exch $0
FunctionEnd

; Pops a path and pushes 1 when the file exists but cannot be opened for
; writing the way File opens it, 0 otherwise.
Function ${UN}AstrLinkFileBusy
  Exch $0
  Push $1
  StrCpy $1 0
  ${If} ${FileExists} "$0"
    ; GENERIC_WRITE, FILE_SHARE_READ, OPEN_EXISTING: never creates or
    ; truncates the file.
    System::Call 'kernel32::CreateFileW(w r0, i 0x40000000, i 1, p 0, i 3, i 0x80, p 0) i .r1'
    ${If} $1 = -1
      StrCpy $1 1
    ${Else}
      System::Call 'kernel32::CloseHandle(p r1)'
      StrCpy $1 0
    ${EndIf}
  ${EndIf}
  StrCpy $0 $1
  Pop $1
  Exch $0
FunctionEnd
!macroend
!insertmacro ASTRLINK_PROCESS_FUNCTIONS ""
!insertmacro ASTRLINK_PROCESS_FUNCTIONS "un."

; Waits, within the shared deadline in $R4 (quarter seconds), until
; $INSTDIR\FILE can be written.
!macro AstrLinkWaitForFile FILE
  ${Do}
    Push "$INSTDIR\${FILE}"
    Call ${ASTRLINK_FN}AstrLinkFileBusy
    Pop $R0
    ${If} $R0 = 0
    ${OrIf} $R4 >= 80
      ${ExitDo}
    ${EndIf}
    ${If} $R5 != "${FILE}"
      DetailPrint "Waiting for ${FILE} to be released..."
      StrCpy $R5 "${FILE}"
    ${EndIf}
    Sleep 250
    IntOp $R4 $R4 + 1
  ${Loop}
!macroend

!macroundef CheckIfAppIsRunning
!macro CheckIfAppIsRunning executableName productName
  ; Only the current user's processes are matched; a per-machine install
  ; would also need other users' copies to exit.
  !if "${INSTALLMODE}" != "currentUser"
    !error "CheckIfAppIsRunning expects a current-user install"
  !endif
  !define /redef ASTRLINK_CHECK_ID ${__LINE__}
  !ifdef __UNINSTALL__
    !define /redef ASTRLINK_FN "un."
  !else
    !define /redef ASTRLINK_FN ""
  !endif
  nsis_tauri_utils::StrReplace "$(appRunning)" "{{product_name}}" "${productName}"
  Pop $R1
  nsis_tauri_utils::StrReplace "$(appRunningOkKill)" "{{product_name}}" "${productName}"
  Pop $R2
  nsis_tauri_utils::StrReplace "$(failedToKillApp)" "{{product_name}}" "${productName}"
  Pop $R3
  Push "${executableName}"
  Push 0
  Call ${ASTRLINK_FN}AstrLinkProcesses
  Pop $R0
  ; An updating app exits by itself right after starting this installer,
  ; once it has stopped Core. Give it and Core's workers time to finish.
  ${If} $UpdateMode = 1
    StrCpy $R4 0
    ${DoWhile} $R0 > 0
      ${If} $R4 >= 40
        ${ExitDo}
      ${EndIf}
      Sleep 250
      IntOp $R4 $R4 + 1
      Push "${executableName}"
      Push 0
      Call ${ASTRLINK_FN}AstrLinkProcesses
      Pop $R0
    ${Loop}
  ${EndIf}
  ${If} $R0 > 0
    IfSilent astrlink_kill_${ASTRLINK_CHECK_ID} 0
    ${IfThen} $PassiveMode != 1 ${|} MessageBox MB_OKCANCEL $R2 IDOK astrlink_kill_${ASTRLINK_CHECK_ID} IDCANCEL astrlink_cancel_${ASTRLINK_CHECK_ID} ${|}
    astrlink_kill_${ASTRLINK_CHECK_ID}:
      Push "${executableName}"
      Push 1
      Call ${ASTRLINK_FN}AstrLinkProcesses
      Pop $R0
      ${If} $R0 > 0
        IfSilent 0 astrlink_ui_${ASTRLINK_CHECK_ID}
          System::Call 'kernel32::AttachConsole(i -1) i .r0'
          ${If} $0 != 0
            System::Call 'kernel32::GetStdHandle(i -11) i .r0'
            System::Call 'kernel32::SetConsoleTextAttribute(i r0, i 0x0004)'
            FileWrite $0 "$R1$\n"
          ${EndIf}
          Abort
        astrlink_ui_${ASTRLINK_CHECK_ID}:
          Abort $R3
      ${EndIf}
      Goto astrlink_checked_${ASTRLINK_CHECK_ID}
    astrlink_cancel_${ASTRLINK_CHECK_ID}:
      Abort $R1
  ${EndIf}
  astrlink_checked_${ASTRLINK_CHECK_ID}:
  ; An exited process can keep its image mapped for a moment, and security
  ; software may still hold a file it is scanning. Wait until every file the
  ; installer replaces can be written; File reports anything still locked.
  StrCpy $R4 0
  StrCpy $R5 ""
  !insertmacro AstrLinkWaitForFile "${executableName}"
  !insertmacro AstrLinkWaitForFile "astrlink-core.exe"
  !insertmacro AstrLinkWaitForFile "astrlink-privacy-worker.exe"
  !insertmacro AstrLinkWaitForFile "astrlink-classifier-worker.exe"
  !insertmacro AstrLinkWaitForFile "astrlink-cli.exe"
  !insertmacro AstrLinkWaitForFile "DirectML.dll"
!macroend
