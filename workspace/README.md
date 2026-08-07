# workspace/

This directory serves as the harness's workspace. Anything placed in here will be accessible via e.g. `tau shell` or any process, including Pi, running in the container.

This directory is a live mount. Edits are bidirectional; any coding agent running inside the container has full read/write access to this directory. Take proper precautions.

You probably want to manage this directory via `tau workspace`.

