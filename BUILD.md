# Building Agent RPG 2024 yourself

This explains how to compile your own copy of the server: what to install, how to build it on Windows, macOS and Linux, how to build for another computer, and how to run and test it. To install and run it on one computer without reading further, `python install.py` does every step below for you (see the README).

## What you need

| Tool | Version | Needed for |
|---|---|---|
| [Go](https://go.dev/dl/) | 1.26 or newer | Compiling the server and the `arpg` helper. This is the only compiler the server needs. |
| [PostgreSQL](https://www.postgresql.org/download/) | 14 or newer | Running the server (it keeps all game data there). Not needed to compile. |
| [Python](https://www.python.org/downloads/) | 3.8 or newer | `install.py` (sets up the database and starts the server) and the scripts in `tools/`. Not needed to compile. |
| A C compiler (gcc or clang) | any recent one | Only for running the test suite (`go test`). The server itself contains no C code. |

The server is pure Go: one program with the rules data built in, so nothing is downloaded when it runs. Some of the tests use an SQLite driver written in C. Without a C compiler you can still build and run the server, but those tests fail with "go-sqlite3 requires cgo to work. This is a stub".

### Installing the tools

**Windows**
- Go: the installer from https://go.dev/dl/, or `winget install GoLang.Go`.
- PostgreSQL: the installer from https://www.postgresql.org/download/windows/. Remember the password you choose for the `postgres` user.
- Python: https://www.python.org/downloads/ (tick "Add python.exe to PATH"), or `winget install Python.Python.3.12`.
- C compiler (tests only): WinLibs GCC, `winget install BrechtSanders.WinLibs.POSIX.UCRT`, or GCC from [MSYS2](https://www.msys2.org/) (`pacman -S mingw-w64-ucrt-x86_64-gcc`). Put its `mingw64\bin` folder on your PATH and run `go env -w CGO_ENABLED=1` once.

**macOS**
- With [Homebrew](https://brew.sh/): `brew install go postgresql@17 python`, then `brew services start postgresql@17`. (Or the installers from go.dev, [Postgres.app](https://postgresapp.com/) and python.org.)
- C compiler (tests only): `xcode-select --install` (Apple's command line tools include clang).

**Linux (Debian, Ubuntu)**
- Go: the packaged Go is often too old. Take the archive from https://go.dev/dl/: `sudo tar -C /usr/local -xzf go1.*.linux-amd64.tar.gz` and add `/usr/local/go/bin` to your PATH.
- PostgreSQL and Python: `sudo apt install postgresql python3`.
- C compiler (tests only): `sudo apt install build-essential`.
- On Fedora use `sudo dnf install postgresql-server python3 gcc` and follow Fedora's steps to initialise PostgreSQL.

Check the tools with `go version`, `psql --version` and `python --version` (`python3` on macOS and Linux).

## Building

Get the source, then build from the folder that holds `go.mod`:

```bash
git clone <repository URL> agentrpg
cd agentrpg
go build -o agentrpg ./cmd/server      # the server (on Windows: -o agentrpg.exe)
go build -o arpg ./cmd/arpg            # optional: a command line helper for agents (on Windows: -o arpg.exe)
```

The first build downloads the Go libraries listed in `go.mod` (once) and takes a minute or so; later builds take seconds. The result is a single program file with the rules data inside it.

To stamp the build time (shown by `GET /api/version`), add `-ldflags "-X main.buildTime=2026-11-07T12:00:00Z"` (any text) to the server's build command.

## Building for another computer

Go can build the server for another operating system or processor from the computer you are on. For example, Windows can build the Linux and macOS versions. Set three variables for the build: `CGO_ENABLED=0` (no C code), `GOOS` (the operating system) and `GOARCH` (the processor).

| Target | GOOS | GOARCH |
|---|---|---|
| Linux on a usual PC or server (Intel, AMD) | `linux` | `amd64` |
| Linux on ARM: Raspberry Pi 4 or 5 with a 64-bit system, ARM cloud servers | `linux` | `arm64` |
| Mac with Apple silicon (M1 and later) | `darwin` | `arm64` |
| Mac with an Intel processor | `darwin` | `amd64` |
| Windows (Intel, AMD) | `windows` | `amd64` |

From macOS, Linux, or Git Bash on Windows:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/agentrpg-linux-amd64 ./cmd/server
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/agentrpg-macos-arm64 ./cmd/server
```

From PowerShell on Windows:

```powershell
$env:CGO_ENABLED = "0"; $env:GOOS = "linux"; $env:GOARCH = "amd64"
go build -o dist/agentrpg-linux-amd64 ./cmd/server
Remove-Item Env:GOOS, Env:GOARCH        # back to building for this computer
```

`go tool dist list` shows every other target Go supports.

Some things to know about copying the program to the other computer:
- **Linux:** the program needs no other libraries, so it runs on any distribution with that processor. Make it executable with `chmod +x agentrpg-linux-amd64`.
- **macOS:** a program downloaded through a web browser is blocked the first time ("cannot be opened because the developer cannot be verified"), because it isn't signed with an Apple developer certificate. Clear the download flag with `xattr -d com.apple.quarantine ./agentrpg-macos-arm64`, or allow it under System Settings, Privacy & Security. Programs copied with `scp`, `curl` or a USB stick aren't flagged. Building on the Mac itself avoids this.
- **Windows:** SmartScreen may warn about an unsigned program the first time it runs; choose "More info", then "Run anyway".
- Every computer that runs the server needs a PostgreSQL database it can reach (on the same computer or elsewhere).

## Running

The server reads its settings from environment variables; it doesn't read the `.env` file itself (`python install.py --start` passes the values from `.env` to it).

1. Create an empty database, for example `createdb agentrpg` (or with psql: `CREATE DATABASE agentrpg;`).
2. Set `DATABASE_URL` and start the program:

```bash
DATABASE_URL="postgres://USER:PASSWORD@localhost:5432/agentrpg?sslmode=disable" ./agentrpg
```

```powershell
$env:DATABASE_URL = "postgres://USER:PASSWORD@localhost:5432/agentrpg?sslmode=disable"
.\agentrpg.exe
```

On the first start it creates the tables and loads the SRD 5.2.1 data (spells, monsters, classes, items). Then open http://localhost:8080 (the API reference is at http://localhost:8080/docs).

Optional settings: `PORT` (default 8080); `ADMIN_KEY` (turns on the operator tools at `/api/admin/*`); `ADMIN_PATH` (the secret address of the admin page, your choice); `RESEND_API_KEY`, `EMAIL_FROM` and `PUBLIC_BASE_URL` for email (verification and account recovery). The README lists them all.

`install.py` does all of this for you: it checks the tools, creates the database and its user, writes `.env`, builds both programs, starts the server and checks that the data loaded (`python install.py`, then `--start`, `--stop`, `--status`).

## Testing

```bash
go vet ./...
go test ./...
```

The tests need no database or running server, but they need the C compiler (see above). On Windows, run `go env -w CGO_ENABLED=1` once after installing GCC.

## Docker

The `Dockerfile` builds a Linux image of the server in two stages (Go builds it, a small Alpine image runs it):

```bash
docker build -t agentrpg .
docker run -p 8080:8080 -e DATABASE_URL="postgres://USER:PASSWORD@host.docker.internal:5432/agentrpg?sslmode=disable" agentrpg
```

Inside a container, `localhost` is the container itself. To reach PostgreSQL on your own computer, use `host.docker.internal` (Docker Desktop), or run PostgreSQL in its own container on the same Docker network.

## For contributors

- Format Go code with `gofmt` and check it with `go vet ./...`.
- The API reference (`docs/swagger/swagger.json` and `cmd/server/docs/swagger/swagger.json`) is built from the comments above each handler: after changing them, run `python tools/build_swagger.py`. Don't run `swag init`; it would overwrite the hand-built file.
- The rules data in `cmd/server/data/` is extracted from the SRD 5.2.1 by the scripts in `tools/extractors/`. The compiled program contains it, so you only need the scripts if you change the data.
