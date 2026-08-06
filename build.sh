#!/usr/bin/env bash

set -euo pipefail
cd "$(dirname "$0")"

PI_INSTALL="${PI_INSTALL:-curl -fsSL https://pi.dev/install.sh | sh}"

docker build \
  --build-arg PI_INSTALL="$PI_INSTALL" \
  -t tau:latest \
  .
