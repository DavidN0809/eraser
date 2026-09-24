#!/bin/sh
# Creates only synthetic config and an empty SMTP secret. Never starts delivery.
set -eu
umask 077
mkdir -p secrets
if [ ! -e secrets/config.yaml ]; then cp config.example.yaml secrets/config.yaml; fi
if [ ! -e secrets/smtp-password ]; then : > secrets/smtp-password; fi
chmod 700 secrets
chmod 600 secrets/config.yaml secrets/smtp-password
# Compose file-backed secrets retain host ownership; the process is UID 65532.
if [ "$(id -u)" = 0 ]; then
  chown 65532:65532 secrets/config.yaml secrets/smtp-password
else
  echo 'Run: sudo chown 65532:65532 secrets/config.yaml secrets/smtp-password'
fi
