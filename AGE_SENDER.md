# Sending Kaizen Credentials With age

Use this guide when you already have the Kaizen kubeconfig and need to send it
to a colleague for the dashboard setup.

## Sender Workflow

Install `age` if it is not already available:

```bash
sudo apt install age
# macOS: brew install age
```

Ask the receiver to run:

```bash
./scripts/age_create_identity.sh
```

They should send you only the printed public recipient key. It starts with
`age1...`. Do not ask for their private key.

Encrypt your kubeconfig for that recipient:

```bash
./scripts/age_encrypt_kubeconfig.sh \
    --recipient age1_receiver_public_key_here \
    --input ~/Kaizen_CADS/kubeconfig
```

By default this writes:

```text
~/Kaizen_CADS/kubeconfig.age
```

Send that encrypted `.age` file to the receiver. Do not commit the encrypted
file or the plaintext kubeconfig to git.

If your kubeconfig is somewhere else, pass its path with `--input`:

```bash
./scripts/age_encrypt_kubeconfig.sh \
    --recipient age1_receiver_public_key_here \
    --input /path/to/kubeconfig \
    --out /path/to/kubeconfig.age
```

The receiver can then decrypt it with:

```bash
./scripts/age_decrypt_kubeconfig.sh ~/Downloads/kubeconfig.age
```
