# Repository Guidance

## Shell Portability

- Repo scripts should run on both Linux Bash and macOS system Bash 3.2.
- When using `set -u`, do not iterate an optional/empty array directly with
  `"${array[@]}"`; macOS Bash 3.2 can treat it as an unbound variable. Guard
  first:
  ```bash
  if ((${#array[@]} > 0)); then
      for item in "${array[@]}"; do
          ...
      done
  fi
  ```
- Keep SSH remote snippets compatible with `/bin/sh` unless the remote command
  explicitly invokes Bash. Avoid relying on fragile `sh -c` positional
  arguments over SSH; prefer `sh -s -- arg...` or a fully quoted remote command.
