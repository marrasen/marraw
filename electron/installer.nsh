; Windows Firewall rule for the marraw daemon.
;
; Hosting a library needs inbound connections, and Windows blocks those unless
; a rule allows them. Without one, Windows raises a Security Alert the first
; time the daemon listens -- easy to miss, easy to decline, and a declined
; prompt leaves the app looking healthy while being unreachable.
;
; One rule covers everything the daemon does, because discovery moved into it:
; no protocol is named, so both the TCP port and mDNS on UDP 5353 are allowed
; for that one program.
;
; Scoped to private and domain networks on purpose -- the same default the
; Windows prompt itself offers. A library should not be answering strangers on
; cafe wi-fi, particularly since the pairing endpoints are reachable without a
; credential by design.
;
; This only takes effect when the installer runs elevated, i.e. an "anyone who
; uses this computer" install. A per-user install cannot write firewall rules,
; and falls back to the Windows prompt as before; the netsh call fails
; harmlessly there. Settings -> Remote reports when nothing can find this
; machine either way.

!macro customInstall
  DetailPrint "Allowing marraw through Windows Firewall"
  ; Delete first: an upgrade re-runs this, and rules would otherwise stack up.
  nsExec::Exec 'netsh advfirewall firewall delete rule name="marraw" program="$INSTDIR\resources\marrawd.exe"'
  Pop $0
  ; Failure is not fatal and is deliberately not checked: a per-user install
  ; has no rights to write firewall rules, and the app still works there --
  ; Windows falls back to prompting. Popping the result keeps the stack clean.
  nsExec::Exec 'netsh advfirewall firewall add rule name="marraw" dir=in action=allow program="$INSTDIR\resources\marrawd.exe" enable=yes profile=private,domain'
  Pop $0
!macroend

!macro customUnInstall
  nsExec::Exec 'netsh advfirewall firewall delete rule name="marraw" program="$INSTDIR\resources\marrawd.exe"'
  Pop $0
!macroend
