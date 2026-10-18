#!/bin/sh
# Generate the disposable validation TLS certificate. Validation only.
set -eu
cd "$(dirname "$0")/.."
mkdir -p edge/certs
if [ -f edge/certs/validation.crt ] && [ -f edge/certs/validation.key ]; then
  exit 0
fi
openssl req -x509 -nodes -newkey rsa:2048 -days 2 \
  -keyout edge/certs/validation.key \
  -out edge/certs/validation.crt \
  -subj "/CN=validation.localhost" \
  -addext "subjectAltName=DNS:validation.localhost,DNS:api.validation.localhost" >/dev/null 2>&1
chmod 600 edge/certs/validation.key
echo "generated disposable TLS certificate in edge/certs"
