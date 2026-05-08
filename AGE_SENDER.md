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

They can send you the public key by email, chat, or SSH. If they know your email
address, they can open a prefilled email draft:

```bash
./scripts/age_create_identity.sh --mailto your.address@example.com
```

If they can SSH to your machine, they can send the public key directly:

```bash
./scripts/age_create_identity.sh --send-to your_user@your_host
```

That uses the normal `ssh` password prompt when needed and writes their public
key to `~/.config/cads/age-recipient.txt` on your machine.

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

If the receiver will fetch the encrypted file from your machine with
`--get-from`, leave it at the default path:

```text
~/Kaizen_CADS/kubeconfig.age
```

If your kubeconfig is somewhere else, pass its path with `--input`:

```bash
./scripts/age_encrypt_kubeconfig.sh \
    --recipient age1_receiver_public_key_here \
    --input /path/to/kubeconfig \
    --out /path/to/kubeconfig.age
```

If the receiver used `--send-to`, use the recipient file written on your
machine:

```bash
./scripts/age_encrypt_kubeconfig.sh \
    --recipient-file ~/.config/cads/age-recipient.txt \
    --input ~/Kaizen_CADS/kubeconfig
```

The receiver can then decrypt it with:

```bash
./scripts/age_decrypt_kubeconfig.sh ~/Downloads/kubeconfig.age
```

Or, if they can SSH to your machine and the encrypted file is still there:

```bash
./scripts/age_decrypt_kubeconfig.sh --get-from your_user@your_host
```
