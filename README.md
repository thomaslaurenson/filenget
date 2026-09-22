# filenget

![Release Build](https://img.shields.io/github/actions/workflow/status/thomaslaurenson/filenget/tag.yml?style=flat&label=release&logo=github) ![Main Build](https://img.shields.io/github/actions/workflow/status/thomaslaurenson/filenget/main.yml?style=flat&label=main&logo=github)

![Release Version](https://img.shields.io/github/v/release/thomaslaurenson/filenget?style=flat&logo=github) ![Release downloads](https://img.shields.io/github/downloads/thomaslaurenson/filenget/total?style=flat&label=downloads&logo=github)

![Go Version](https://img.shields.io/github/go-mod/go-version/thomaslaurenson/filenget?style=flat&logo=go) ![Code Coverage](https://img.shields.io/badge/Coverage-91.6%25-blue?style=flat&logo=go)

Download and decrypt the files behind a Filen public share link, from the command line.

> filenget is an independent tool, not affiliated with or endorsed by Filen.

Filen shares are end-to-end encrypted and the key lives in the link's URL fragment, which a browser never sends, so `curl` on a share link retrieves Filen's web app rather than the file. filenget does what that app does: it asks the public-link API where the file is, decrypts the metadata with the key from the fragment, then fetches and decrypts the chunks. Build scripts and CI can therefore fetch shared files with no Filen account.

## Installation

Download a pre-built binary from the [releases page](https://github.com/thomaslaurenson/filenget/releases). For an easier install, use the bash installer script:

```sh
curl -fsSL https://github.com/thomaslaurenson/filenget/releases/latest/download/install.sh | bash
```

Or the PowerShell installer script if on Windows:

```ps
irm https://github.com/thomaslaurenson/filenget/releases/latest/download/install.ps1 | iex
```

Install from source:

```sh
go install github.com/thomaslaurenson/filenget@latest
```

## Usage

```
filenget <directory> <link>...
filenget --version
```

A link is either a file link, `https://drive.filen.io/d/<uuid>#<key>`, or a folder link, `https://drive.filen.io/f/<uuid>#<key>`. A folder link fetches every file directly inside the folder under its own name; subfolders are not descended.

A file already at the destination is left alone, so running the same command again picks up whatever a previous run did not reach; a file interrupted part-way through is fetched again from the start. Each download is written to a `.part` file and renamed only once every chunk has arrived and the decrypted byte count matches the size the metadata promised, so a partial file is never mistaken for a complete one.

A file that fails does not abandon the ones behind it: every link is attempted and the failures reported together. Chunks are fetched several at a time, and one lost to a dropped connection or a server-side error is retried.

One line per file goes to stderr and stdout stays empty, so `filenget testdata <link> > out.txt` leaves `out.txt` empty and a script can rely on the exit code alone.

| Code | Meaning |
|---|---|
| 0 | every file was downloaded or already present |
| 1 | a failure, with a message on stderr |
| 130 | interrupted with Ctrl-C, no message |

### Examples

```bash
# Fetch everything behind a folder link into ./testdata
filenget testdata https://drive.filen.io/f/<uuid>#<key>

# Fetch a single file
filenget testdata https://drive.filen.io/d/<uuid>#<key>

# Fetch several links into the same directory
filenget testdata https://drive.filen.io/f/<uuid>#<key> https://drive.filen.io/d/<uuid>#<key>
```

### Shell completion

`filenget completion <bash|zsh|fish|powershell>` prints the completion script for a shell. Where it is installed is up to you; the installer scripts deliberately leave it alone.

## Supported shares

Only version 2 metadata and file data are implemented, which is what Filen's SDK describes for the links in circulation. Version 3 exists there, with hex keys and a `003` marker, and a share using it is rejected by name rather than guessed at. Password-protected links are not supported, and are rejected by name too.

The implementation follows Filen's open-source SDK, `filen-sdk-rs/src/crypto/v2.rs` in [FilenCloudDienste/filen-rs](https://github.com/FilenCloudDienste/filen-rs). Metadata is decrypted with AES-256-GCM under a key stretched by PBKDF2-SHA512, and each chunk with AES-256-GCM under the file's own key. GCM authenticates before it returns anything, so the wrong key fails outright rather than writing a file of rubbish.
