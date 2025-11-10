# Environment Preparation

Use this document when you want to understand or customize what `prepare.sh`
does. For the default workflow you only need to run:

```bash
./prepare.sh --platform linux     # auto-detects when omitted
```

That command:

1. Installs the required Debian/Ubuntu packages via `apt-get`.
2. Ensures Go ≥ 1.22.2 is installed (downloads the official tarball when
   necessary).
3. Installs FMIL (fmilib) under `./.fmil/` by invoking
   `scripts/install_fmil.sh` (idempotent).

After the script completes, `./build.sh` can build FMUs, compile the Go binaries,
and create the container images without any additional manual setup.

---

## Manual Go installation

If you prefer to manage Go yourself, install version 1.22.2 or newer and ensure
`go` is on your `PATH`. The official tarball workflow mirrors what
`prepare.sh` does:

```bash
GO_VERSION=1.22.2
GO_ARCH=linux-amd64   # use linux-arm64 on ARM hosts
curl -fsSL "https://go.dev/dl/go${GO_VERSION}.${GO_ARCH}.tar.gz" -o /tmp/go.tgz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go.tgz
rm -f /tmp/go.tgz
```

Add `/usr/local/go/bin` (and optionally `~/go/bin`) to your `PATH`.

---

## Manual FMIL installation

`scripts/install_fmil.sh` clones and installs fmilib automatically, but you can
replicate its steps manually if desired:

```bash
PREFIX=$PWD/.fmil
git clone https://github.com/modelon-community/fmi-library.git /tmp/fmi-library
cmake -S /tmp/fmi-library -B /tmp/fmi-library/build \
      -DCMAKE_BUILD_TYPE=Release \
      -DCMAKE_INSTALL_PREFIX="$PREFIX"
cmake --build /tmp/fmi-library/build -j"$(nproc)"
cmake --install /tmp/fmi-library/build
```

The Go tooling requires:

- Headers under `$PREFIX/include`
- Shared libraries under `$PREFIX/lib`

Point `build.sh` (or your shell) at the custom prefix via `--fmil-home` or the
`FMIL_HOME` environment variable.

---

## Environment variables (advanced)

When you rely on the `.fmil/` directory created by `prepare.sh` / `build.sh`,
no extra exports are necessary. If you want to reuse a system FMIL install,
define:

```bash
export FMIL_HOME=/path/to/fmil
export LD_LIBRARY_PATH="$FMIL_HOME/lib:$LD_LIBRARY_PATH"
export PKG_CONFIG_PATH="$FMIL_HOME/lib/pkgconfig:$PKG_CONFIG_PATH"
export CGO_ENABLED=1
export CGO_CFLAGS="-I$FMIL_HOME/include"
export CGO_CXXFLAGS="-I$FMIL_HOME/include"
export CGO_LDFLAGS="-L$FMIL_HOME/lib"
export GOWORK=off
```

`build.sh --fmil-home /path/to/fmil` is a convenient alternative when you only
need to override the prefix occasionally.
