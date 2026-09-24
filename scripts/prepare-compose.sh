#!/bin/sh
# Creates only synthetic config and empty SMTP/search secrets. Never starts delivery.
set -eu
umask 077
mkdir -p secrets
if [ ! -e secrets/config.yaml ]; then cp config.example.yaml secrets/config.yaml; fi
if [ ! -e secrets/smtp-password ]; then : > secrets/smtp-password; fi
if [ ! -e secrets/discovery-api-key ]; then : > secrets/discovery-api-key; fi
chmod 700 secrets
chmod 600 secrets/config.yaml secrets/smtp-password secrets/discovery-api-key
# Compose file-backed secrets retain host ownership; the process is UID 65532.
if [ "$(id -u)" = 0 ]; then
  chown 65532:65532 secrets/config.yaml secrets/smtp-password secrets/discovery-api-key
  # Let the invoking Compose client traverse the directory without exposing file contents.
  if [ -n "${SUDO_UID:-}" ] && [ -n "${SUDO_GID:-}" ]; then
    chown "${SUDO_UID}:${SUDO_GID}" secrets
  fi
else
  echo 'Run: sudo chown 65532:65532 secrets/config.yaml secrets/smtp-password secrets/discovery-api-key'
fi
