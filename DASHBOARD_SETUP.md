# Local Dashboard Setup

This guide is for users who want to run their own local browser dashboard while
launching workflows into the shared Kaizen Argo playground.

For the OS-independent user paths, see [`USER_PATHS.md`](USER_PATHS.md).

The dashboard itself runs on your laptop at `http://localhost:8080/`. Workflow
pods run remotely in Kaizen using the configured container image.
You can launch new workflow runs from the dashboard and inspect recent runs.

## Prerequisites

- Bash shell on Linux or macOS
- Git
- Go is installed locally by the project helpers when needed
- A local container engine only when building/publishing images or using local
  Minikube development
- Access to the Kaizen Argo playground through either an Argo bearer token or a
  kubeconfig
- Optional: a published workflow image tag if you want this dashboard to launch
  the exact same workflows as another machine
- GitHub/GHCR package-write access only if you need to publish a freshly built
  workflow image

### Host Preparation

Use the same prepare command on Linux and macOS:

```bash
./prepare.sh
```

The default path prepares hosted dashboard client dependencies only. It does not
start a local cluster, and it does not require Podman/Docker unless you later
build/publish images.

On Debian/Ubuntu, `prepare.sh` can install package prerequisites through
`apt-get`. On macOS, install host tools such as `age`, Git, and Podman/Docker
with Homebrew, MacPorts, or the normal desktop installer.

`podman info` or `docker info` must succeed only for build/publish work or
local Minikube development. The connect-only dashboard path does not need a
container runtime.

If Docker Hub or GHCR pulls fail with an unknown certificate authority error,
copy your corporate CA certificates into `scripts/certs/` and sync them into the
Podman VM:

```bash
scripts/install_podman_ca.sh --cert-dir scripts/certs
podman pull docker.io/library/python:3.11-slim
```

## 1. Clone The Repo

```bash
git clone https://github.com/janlv/cads-fmi-demo.git
cd cads-fmi-demo
```

## 2. Put Credentials Outside Git

Use one of these Kaizen access methods.

Argo token:

```bash
export ARGO_TOKEN=...
```

Kubeconfig at the default path:

```bash
mkdir -p ~/Kaizen_CADS
# place the playground kubeconfig at:
# ~/Kaizen_CADS/kubeconfig
```

Explicit kubeconfig path:

```bash
./run_playground.sh --kubeconfig /path/outside/this/repo/kubeconfig
```

The launcher resolves Kaizen credentials in this order:

1. `ARGO_TOKEN`
2. `KUBECONFIG`
3. `--kubeconfig ...`
4. `~/Kaizen_CADS/kubeconfig`

### Optional: Encrypted Kubeconfig Handoff With age

Use this when one colleague needs to send another colleague a kubeconfig without
emailing the credential in plaintext.

The receiving colleague first installs `age` on the dashboard machine, creates
an identity there, and sends only the printed public recipient key:

```bash
sudo apt install age
# macOS: brew install age
./scripts/age_create_identity.sh
```

Then they send the public key to the credential sender using one of these
options.

Open a prefilled email draft:

```bash
./scripts/age_create_identity.sh --mailto sender@example.com
```

Copy the public key to the clipboard:

```bash
./scripts/age_create_identity.sh --copy
```

Send the public key directly to the sender's machine over SSH:

```bash
./scripts/age_create_identity.sh --send-to sender_user@sender_host
```

The SSH command sends only the public key. It uses the normal `ssh` password
prompt when needed and saves the key on the remote host as
`~/.config/cads/age-recipient.txt`, which the sender can pass to
`age_encrypt_kubeconfig.sh --recipient-file`.

The output includes a public key beginning with `age1...`. That public key is
safe to send back to the person who has the kubeconfig. The private key stays on
the dashboard machine in `~/.config/age/key.txt`. The public key is also stored
at `~/.config/cads/age-recipient.txt` so the decrypt helper can use it later.
If that public key file is missing but the private key exists, the decrypt
helper recreates it locally.

The person who has the kubeconfig should follow
[`AGE_SENDER.md`](AGE_SENDER.md). They will either send back an encrypted
`.age` file or keep their plaintext kubeconfig available for the receiver's
`--get-from` SSH flow. Do not commit kubeconfigs or encrypted handoff files to
git.

The receiving colleague decrypts it into the dashboard default location:

```bash
./scripts/age_decrypt_kubeconfig.sh ~/Downloads/kubeconfig.age
```

If the plaintext kubeconfig stays on the sender's machine and the receiver has
SSH access, they can encrypt it remotely and decrypt it locally:

```bash
./scripts/age_decrypt_kubeconfig.sh --get-from sender_user@sender_host
```

This uses the stored public key, asks `ssh` for the remote password if needed,
and runs `age` on the sender host. The remote kubeconfig path defaults to
`~/Kaizen_CADS/kubeconfig`. Use `--remote-path /path/to/kubeconfig` if the
sender stores it somewhere else.

They can then run:

```bash
./run_playground.sh
```

The scripts are thin wrappers around `age`; they do not store keys in the repo.
The generated private key, decrypted kubeconfig, and encrypted handoff file stay
under the user's home directory by default.

## 3. Choose A User Path

### Playground Dashboard

Use this when another machine, such as your Linux PC, already published the
workflow image and you want the Mac dashboard to connect to the same Playground
environment.

On the machine that already works, print the image tag:

```bash
source .local/state/dashboard-remote-image.env
printf '%s\n' "$cached_image"
```

On this machine, start the dashboard without building or publishing:

```bash
./run_playground.sh
```

If you need to override the configured image:

```bash
./run_playground.sh --image ghcr.io/org/cads-fmi-demo:tag
```

### Publish To Playground

The dashboard can reuse an already published image. In that case, Kaizen
credentials are enough.

If the repository has local changes and the launcher needs to build and publish
a new image, authenticate your local machine to GHCR with package-write access:

```bash
./run_publish.sh
```

`run_publish.sh` uses `GHCR_TOKEN`, `GITHUB_TOKEN`, or your existing `gh`
login to authenticate GHCR when needed. These tokens are used only by the local
container engine when pushing the image.

Publishing updates the configured Playground image tag with a full repo image.
It does not publish just one workflow file. If you replaced one mock workflow,
test that one workflow with Local Dev first, then run:

```bash
./run_publish.sh
```

### Local Dev

Use this when you want quick CLI-only local workflow/model testing without
Kaizen Playground, GHCR publishing, or a dashboard:

```bash
./run_local_dev.sh workflows/python_chain.yaml
```

## 4. Build Demo FMUs When Publishing Or Developing Locally

Playground Dashboard users who connect to an existing published image do not need to build
FMUs locally. If you use the Publish to Playground or Local Dev path from a fresh checkout,
build the Python FMUs once:

```bash
./create_fmu/build_python_fmus.sh
```

The Publish and Local Dev launchers can build the Go service and workflow image
when needed, but FMUs still need to exist under `fmu/models/` for workflows that
reference them.

## 5. Start The Dashboard

Default Playground launch:

```bash
./run_playground.sh
```

Then open:

```text
http://localhost:8080/
```

Useful variants:

```bash
# Inspect existing runs without launching new workflows from a pinned image.
./run_playground.sh

# Build/publish a new Playground image and start the dashboard.
./run_publish.sh

# Use a non-default port.
./run_playground.sh --addr :8081

# Local workflow/model development without Playground.
./run_local_dev.sh workflows/python_chain.yaml
```

## Credential Handling

Do not commit any of these to the repository:

- `ARGO_TOKEN`
- kubeconfig files
- playground bearer tokens
- S3 keys or bucket credentials
- `GHCR_TOKEN` or `GITHUB_TOKEN`
- Docker or Podman registry auth files

At the time this was checked, GitHub reported this repository as public:

```bash
gh repo view janlv/cads-fmi-demo --json visibility,isPrivate
```

Even if the repository is later made private, do not store shared playground
credentials in git. Secrets can leak through history, forks, logs, container
layers, caches, and local clones. Prefer environment variables, files under
`~/Kaizen_CADS/`, or an institutional secret manager.

If a real credential has ever been committed or pushed, treat it as compromised:
rotate it, remove it from current files, and clean history only as a follow-up
containment step. History rewriting does not revoke a leaked credential.

## Troubleshooting

Check whether the dashboard can reach the playground:

```bash
curl -fsS http://localhost:8080/api/config | python3 -m json.tool
```

Expected healthy state:

```json
{
  "remoteEnabled": true,
  "problems": null
}
```

If `remoteEnabled` is `false`, inspect the `problems` field. Common causes are:

- `argo` is not on `PATH`
- no `ARGO_TOKEN` or kubeconfig was found
- the kubeconfig points at the wrong cluster or namespace
- the selected remote image is not published or cannot be pulled by Kaizen

To stop an old dashboard occupying port `8080`, rerun `./run_playground.sh`; the
launcher stops previous dashboard sessions for this repo automatically. If a
non-dashboard process owns the port, either stop that process or use:

```bash
./run_playground.sh --addr :8081
```
