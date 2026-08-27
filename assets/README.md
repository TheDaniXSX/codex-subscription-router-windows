# Launcher icon

`codex-color.svg` is the Codex color mark from
[Lobe Icons](https://github.com/lobehub/lobe-icons), used under its MIT
license. `codex-color.ico` is a Windows multi-resolution rendering of that SVG.

The Windows resource definition in `cmd/windows-launcher/windows_resources.rc`
embeds the ICO in the independent Go launcher. The generated COFF resource is
x64-only because the current Windows compatibility contract is x64-only.

Codex and the Codex mark belong to OpenAI. Their presence identifies the
compatible product and does not imply that this unofficial router is published,
endorsed, or supported by OpenAI.
