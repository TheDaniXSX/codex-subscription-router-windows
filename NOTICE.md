# Notices

This repository contains original patching and multiplexing source. It does not
include and must not be used to redistribute the official Codex desktop application,
its ASAR archive, embedded native services, or other OpenAI binaries.

ChatGPT, Codex, OpenAI, and related names and marks belong to OpenAI. Their use
here identifies compatibility only and does not imply affiliation,
endorsement, or support.

The launcher icon is sourced from the Lobe Icons project and used under the
MIT License. Copyright (c) 2023 LobeHub. See `assets/LOBE-ICONS-LICENSE.txt`.
The Codex mark depicted by that icon belongs to OpenAI.

Usage history uses the Go SQLite driver `modernc.org/sqlite` v1.38.2 under
the BSD 3-Clause License. Copyright (c) 2017 The Sqlite Authors. See
`assets/SQLITE-LICENSE.txt` for the driver, SQLite public-domain statement,
all pinned transitive Go dependency licenses, embedded libc/memory notices,
and the Go runtime license. That notice bundle is also copied into the installed
app's `resources/codex-router` directory. The pinned dependency versions are
recorded in `go.mod` and `go.sum`; the driver does not require a separate SQLite
DLL or CGO.

The build process modifies a local copy of software supplied separately by the
user. The user is responsible for complying with the licenses and terms that
apply to that software and to each connected subscription.
