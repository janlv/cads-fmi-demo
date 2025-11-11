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
3. Installs FMIL (fmilib) under `./.local/` (default) or `/usr/local/fmil`
   (`--global`) by invoking `scripts/install_fmil.sh` (idempotent).
4. Installs the Argo CLI (default `v3.5.6`) under `./.local/bin/argo`
   (`--local`) or `/usr/local/bin/argo` (`--global`) so `run.sh` (default
   `--mode argo`) works out of the box.
5. Installs `kubectl` (`v1.30.0`) and Minikube (`v1.33.1`) under the same
   scope and boots a local Minikube cluster (driver `docker`) unless
   `MINIKUBE_AUTO_START=false`.

After the script completes, `./build.sh` can build FMUs, compile the Go binaries,
and create the container images without any additional manual setup.

---

## Local vs global installation

`prepare.sh` installs everything in *local* mode by default, which keeps FMIL
under `./.local/` (headers/libraries) and the Argo CLI under `./.local/bin/argo`. This path avoids
sudo and the helper scripts prepend `./.local/bin` to `PATH`, but add it to your
shell profile if you want `argo` available everywhere.

Pass `--global` when you prefer system-wide installs. In that mode FMIL lands in
`/usr/local/fmil`, the CLIs (`argo`, `kubectl`, `minikube`) are installed to
`/usr/local/bin`, and sudo is required. You can override the exact destinations
via `FMIL_HOME`, `ARGO_INSTALL_PATH`, `KUBECTL_INSTALL_PATH`, and
`MINIKUBE_INSTALL_PATH` regardless of the scope flag.

---

## Local Kubernetes automation (Minikube)

To keep the demo self-contained, `prepare.sh` installs `kubectl` and Minikube
and then starts a local Kubernetes cluster:

- Override tool versions via `KUBECTL_VERSION` / `MINIKUBE_VERSION`.
- Control the driver via `MINIKUBE_DRIVER`, `prepare.sh --podman`, or
  `prepare.sh --docker`. The default is `podman` (rootless-friendly); switch to
  `docker`, `qemu`, etc., as needed.
- Skip automatic `minikube start` by exporting `MINIKUBE_AUTO_START=false`. The
  CLIs remain installed so you can point at an existing cluster manually.
- When the requested driver is unavailable (e.g., Podman not installed or Docker
  is just a shim), the script automatically swaps to the other option if
  available and logs the decision.
- Before any Kubernetes jobs run, `run.sh` calls `scripts/install_minikube_ca.sh`
  to copy TLS inspection certificates into the Minikube VM. Every `.crt`/`.pem`
  under `scripts/certs/` is installed automatically; set
  `MINIKUBE_EXTRA_CA_CERT=/path/to/file.crt` (and optional
  `MINIKUBE_EXTRA_CA_NAME`) for single files or
  `MINIKUBE_EXTRA_CA_CERTS_DIR=/custom/dir` to change the directory that gets
  scanned. This keeps `quay.io`/`registry.k8s.io` pulls working behind corporate
  MITM proxies.

The generated kubecontext (`minikube`) lives in your default kubeconfig, so
`kubectl config current-context` succeeds and `run.sh` can submit workflows
without extra steps. If you later change `KUBECONFIG` to target another cluster,
`run.sh` will use that context instead.

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
PREFIX=$PWD/.local
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

## Manual Argo CLI installation

`prepare.sh` fetches the Argo Workflows CLI (`argo`) from the official GitHub
releases (matching your CPU/OS) and installs it to either `./.local/bin/argo`
(`--local`, default) or `/usr/local/bin/argo` (`--global`). If you prefer manual
control:

```bash
ARGO_VERSION=v3.5.6
curl -fsSL "https://github.com/argoproj/argo-workflows/releases/download/${ARGO_VERSION}/argo-linux-amd64.gz" -o /tmp/argo.gz
gunzip /tmp/argo.gz
chmod +x /tmp/argo
sudo install -m 0755 /tmp/argo /usr/local/bin/argo
```

Swap `linux-amd64` with `linux-arm64`, `darwin-amd64`, or `darwin-arm64` as
needed. Override the script defaults via:

```bash
export ARGO_VERSION_REQUIRED=v3.5.6
export ARGO_INSTALL_PATH=$PWD/.local/bin/argo
```

The Argo CLI operates purely on the client side, so the downloaded binary is
enough to submit/monitor workflows once your `kubectl` context points to the
target cluster. The repo’s `scripts/run_argo_workflow.sh` checks whether the
cluster already hosts the Argo Workflows CRD and (by default) applies the
upstream install manifest if it is missing. Set `ARGO_AUTO_INSTALL=false` to
disable that automation or `ARGO_NAMESPACE=<ns>` / `ARGO_MANIFEST_URL=<url>` to
customize where the controller lives.

---

## Manual kubectl installation

`kubectl` is not available in the default Debian/Ubuntu repositories, so
`prepare.sh` downloads the official release tarball directly. To install it
manually:

```bash
KUBECTL_VERSION=v1.30.0
curl -fsSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux-amd64/kubectl" -o /tmp/kubectl
chmod +x /tmp/kubectl
sudo install -m 0755 /tmp/kubectl /usr/local/bin/kubectl
```

Swap the OS/arch in the URL as needed. Override the automated install path by
setting `KUBECTL_INSTALL_PATH`.

---

## Manual Minikube installation

Minikube also lacks an apt package, so it is fetched from the upstream release
binaries. Manual steps:

```bash
MINIKUBE_VERSION=v1.33.1
curl -fsSL "https://storage.googleapis.com/minikube/releases/${MINIKUBE_VERSION}/minikube-linux-amd64" -o /tmp/minikube
chmod +x /tmp/minikube
sudo install -m 0755 /tmp/minikube /usr/local/bin/minikube
```

Set `MINIKUBE_INSTALL_PATH` to override the destination. After installation run
`minikube start --driver=docker` (or your chosen driver) to provision the local
cluster if you skipped the automated flow.

---

## Environment variables (advanced)

When you rely on the `.local/` directory created by `prepare.sh` / `build.sh`,
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
