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
; The desktop shortcut, named after the product by every release.
!define ASTRLINK_SHORTCUT "AstrLink.lnk"
Var AstrLinkHadShortcut

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
      ; Its uninstaller deletes the desktop shortcut that opens it;
      ; customInstall makes it again for this install.
      ${If} ${FileExists} "$DESKTOP\${ASTRLINK_SHORTCUT}"
        StrCpy $AstrLinkHadShortcut 1
      ${EndIf}
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

; Earlier releases update by running this installer as
; `/P /UPDATE /R /ARGS <their arguments>` and quitting: they ask for no
; questions and for the app to start again afterwards. Such an update
; installs silently and, with /R, opens the app with those arguments.
Var AstrLinkLegacyUpdate

!macro customInit
  Push $0
  Push $1
  ClearErrors
  ${GetParameters} $0
  ${GetOptions} $0 "/UPDATE" $1
  ${IfNot} ${Errors}
    StrCpy $AstrLinkLegacyUpdate 1
    SetSilent silent
  ${EndIf}
  ClearErrors
  Pop $1
  Pop $0
  !insertmacro AstrLinkInstallVcRuntime
  !insertmacro AstrLinkRemoveLegacyInstall
!macroend

!macro customInstall
  ; Make reboot requirements visible to unattended deployment tools as well.
  ${If} ${RebootFlag}
    SetErrorLevel 3010
  ${EndIf}
  ${If} $AstrLinkHadShortcut == 1
  ${AndIfNot} ${FileExists} "$DESKTOP\${ASTRLINK_SHORTCUT}"
    SetOutPath "$INSTDIR"
    CreateShortCut "$DESKTOP\${ASTRLINK_SHORTCUT}" "$INSTDIR\${ASTRLINK_EXE}"
  ${EndIf}
  ${If} $AstrLinkLegacyUpdate == 1
    Push $0
    Push $1
    ClearErrors
    ${GetParameters} $0
    ${GetOptions} $0 "/R" $1
    ${IfNot} ${Errors}
      ; A login launch passed --autostart, which the app still reads.
      ClearErrors
      ${GetOptions} $0 "/ARGS" $1
      ${If} ${Errors}
        StrCpy $1 ""
      ${EndIf}
      Exec '"$INSTDIR\${ASTRLINK_EXE}" $1'
    ${EndIf}
    ClearErrors
    Pop $1
    Pop $0
  ${EndIf}
!macroend

; The VC runtime is shared with other applications. Never uninstall it here.

; ---------------------------------------------------------------------------
; Running AstrLink processes
;
; MyGo's CloseApp quits processes whose path starts with $INSTDIR and goes on
; as soon as they are gone. Core's workers may still be shutting down after
; Core, an exited process can keep its image mapped for a moment, and
; security software may still hold a file it scans, so File could find
; bin\DirectML.dll or an executable still in use and stop with "Error opening
; file for writing".
;
; Before MyGo's section runs, this one waits for the current user's processes
; whose image is AstrLink.exe or one of Core's helpers in $INSTDIR\bin to
; exit, ends those left, and then waits until AstrLink.exe and every file in
; $INSTDIR\bin can be written. Images match by file identity, so a junction,
; subst drive or short name in either path does not hide a running copy.
; Other users' processes and AstrLink copies in other directories are left
; alone. The uninstaller runs the same check before it removes $INSTDIR.
; ---------------------------------------------------------------------------

; MyGo names the executable after the app (name in mygo.config.ts).
!define ASTRLINK_EXE "AstrLink.exe"
; MyGo's own CloseApp texts.
!define ASTRLINK_RUNNING "AstrLink is running. Close it to continue?"
!define ASTRLINK_STUCK "AstrLink is still running. Quit it, then try again."

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

; Pops an action (0 counts, 1 ends the processes). Pushes how many matching
; processes are still running: the current user's whose image is
; $INSTDIR\AstrLink.exe or one of Core's helpers in $INSTDIR\bin.
Function ${UN}AstrLinkProcesses
  Exch $1
  Push $0
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
      ; $0: where this package keeps a program of that name.
      StrCpy $0 ""
      ${If} $6 == "${ASTRLINK_EXE}"
        StrCpy $0 "$INSTDIR\$6"
      ${EndIf}
      ${If} $6 == "astrlink-core.exe"
      ${OrIf} $6 == "astrlink-privacy-worker.exe"
      ${OrIf} $6 == "astrlink-classifier-worker.exe"
      ${OrIf} $6 == "astrlink-cli.exe"
        StrCpy $0 "$INSTDIR\bin\$6"
      ${EndIf}
      ${If} $0 != ""
        ; PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE
        System::Call 'kernel32::OpenProcess(i 0x101000, i 0, i r5) p .r7'
        ${If} $7 <> 0
          System::Call 'kernel32::QueryFullProcessImageNameW(p r7, i 0, w .R0, *i ${NSIS_MAX_STRLEN}) i .r4'
          StrCpy $8 0
          ${If} $4 <> 0
            ${If} $R0 == $0
              StrCpy $8 1
            ${Else}
              Push $0
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
  StrCpy $1 $9
  Pop $R0
  Pop $9
  Pop $8
  Pop $7
  Pop $6
  Pop $5
  Pop $4
  Pop $3
  Pop $2
  Pop $0
  Exch $1
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

; Pops a path and waits, within the deadline shared through $R4 (quarter
; seconds, 20 s in all), until the file can be written.
Function ${UN}AstrLinkWaitForFile
  Exch $0
  Push $1
  ${Do}
    Push $0
    Call ${UN}AstrLinkFileBusy
    Pop $1
    ${If} $1 = 0
    ${OrIf} $R4 >= 80
      ${ExitDo}
    ${EndIf}
    ${If} $R5 != $0
      DetailPrint "Waiting for $0 to be released..."
      StrCpy $R5 $0
    ${EndIf}
    Sleep 250
    IntOp $R4 $R4 + 1
  ${Loop}
  Pop $1
  Pop $0
FunctionEnd

; Waits until AstrLink.exe and every file in $INSTDIR\bin can be written;
; File reports anything still locked after that.
Function ${UN}AstrLinkWaitForFiles
  Push $0
  Push $1
  Push $R4
  Push $R5
  StrCpy $R4 0
  StrCpy $R5 ""
  Push "$INSTDIR\${ASTRLINK_EXE}"
  Call ${UN}AstrLinkWaitForFile
  FindFirst $0 $1 "$INSTDIR\bin\*"
  ${DoWhile} $1 != ""
    ${If} $1 != "."
    ${AndIf} $1 != ".."
    ${AndIfNot} ${FileExists} "$INSTDIR\bin\$1\*.*"
      Push "$INSTDIR\bin\$1"
      Call ${UN}AstrLinkWaitForFile
    ${EndIf}
    FindNext $0 $1
  ${Loop}
  FindClose $0
  Pop $R5
  Pop $R4
  Pop $1
  Pop $0
FunctionEnd

; Ends the running AstrLink processes of this install and waits until their
; files are free.
Function ${UN}AstrLinkStopProcesses
  Push $R0
  Push $R4
  Push 0
  Call ${UN}AstrLinkProcesses
  Pop $R0
  ; An app that quits by itself, and Core's workers after Core, need a
  ; moment; a silent install, as scripts and updates run, waits 10 s for
  ; them before ending them.
  ${If} ${Silent}
    StrCpy $R4 0
    ${DoWhile} $R0 > 0
      ${If} $R4 >= 40
        ${ExitDo}
      ${EndIf}
      Sleep 250
      IntOp $R4 $R4 + 1
      Push 0
      Call ${UN}AstrLinkProcesses
      Pop $R0
    ${Loop}
  ${EndIf}
  ${If} $R0 > 0
    IfSilent astrlink_end
    MessageBox MB_OKCANCEL|MB_ICONEXCLAMATION "${ASTRLINK_RUNNING}" IDOK astrlink_end
    Abort
    astrlink_end:
    Push 1
    Call ${UN}AstrLinkProcesses
    Pop $R0
    ${If} $R0 > 0
      MessageBox MB_OK|MB_ICONSTOP "${ASTRLINK_STUCK}" /SD IDOK
      SetErrorLevel 2
      Quit
    ${EndIf}
  ${EndIf}
  Call ${UN}AstrLinkWaitForFiles
  Pop $R4
  Pop $R0
FunctionEnd
!macroend
!insertmacro ASTRLINK_PROCESS_FUNCTIONS ""
!insertmacro ASTRLINK_PROCESS_FUNCTIONS "un."

; MyGo includes this file ahead of its own section, so this hidden section
; runs first, before CloseApp and the File commands.
Section "-AstrLinkProcesses"
  Call AstrLinkStopProcesses
SectionEnd

; Runs in MyGo's uninstall section, after CloseApp and before $INSTDIR is
; removed.
!macro customUnInstall
  Call un.AstrLinkStopProcesses
!macroend
