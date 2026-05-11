# Dashboard On Existing Playground

Use this path when the workflows have already been built and published by
someone else, and you only want to run the dashboard locally against the same
Kaizen Playground environment.

This is the **Playground Dashboard** path in [`USER_PATHS.md`](USER_PATHS.md).

This path does not build FMUs, does not build a container image, does not push
to GHCR, and does not start Minikube.

## What You Need

- The same Kaizen kubeconfig or Argo token used by the other machine.
- The configured workflow image in `config/playground.env`, unless you need an
  advanced override.

Existing runs in the Playground are stored in Argo, so you can inspect them as
long as your kubeconfig points to the same server and namespace. The configured
image tag only matters when you launch new workflows from the dashboard.

## On The Mac

Prepare only the local dashboard client tools:

```bash
./prepare.sh
```

Fetch the same kubeconfig, for example:

```bash
./scripts/age_decrypt_kubeconfig.sh --get-from user@linux-host
```

Start the dashboard:

```bash
./run_playground.sh
```

Advanced override for a different published image:

```bash
./run_playground.sh --image ghcr.io/org/cads-fmi-demo:tag-from-linux
```

Then open:

```text
http://localhost:8080/
```

The launcher may compile the small local dashboard service binary if it is
missing. That is not a workflow build and does not publish anything.

This can show existing Playground runs if the kubeconfig is valid. New launches
use `CADS_WORKFLOW_IMAGE` from `config/playground.env` unless `--image` is
provided.
