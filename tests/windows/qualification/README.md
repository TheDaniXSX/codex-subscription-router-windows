# Windows automated release gates

The automated gate harness is safe to run on a developer workstation or a
Windows CI runner. It uses only temporary synthetic executables and
`example.invalid` account fixtures. It never starts, stops, patches,
authenticates, or reads account state from the installed Codex application.

Run it from the repository root:

```powershell
pwsh -NoProfile -NonInteractive -File `
  .\tests\windows\release-gates\Invoke-WindowsAutomatedReleaseGates.ps1
```

The default report is written below `artifacts\release-gates`. Supply
`-ReportPath` to create an explicit candidate report. A successful synthetic
gate run is necessary but does not qualify a public release: the generated report lists
the clean-VM, signing, real test-account, desktop UI, Computer Use/Appshots,
extended soak, and distribution gates that still require controlled evidence.

The harness deliberately avoids absolute memory assertions for the real Codex
desktop. Its short soak measures only the mux and fake app-server process tree;
real Electron resource qualification belongs in a stable disposable VM.

The former `Invoke-WindowsReleaseQualification.ps1` path remains as a
compatibility entry point. It runs the same automated gates and never emits a
`QUALIFIED` result; only the canonical, manually completed Windows E2E report
can do that.
