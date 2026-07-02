#!/usr/bin/env bash
set -e

# Cleanup any previous dumps
rm -rf ./pi-journal-new

echo "Extracting journal from /dev/rdisk12s2 (will ask for password)..."
mkdir -p ./pi-journal-new
sudo nix shell nixpkgs#e2fsprogs -c sh -c "debugfs -R 'rdump /var/log/journal ./pi-journal-new' /dev/rdisk12s2"
sudo chown -R "${USER:-USER must be set}" ./pi-journal-new

echo "Loading journal into Docker to read..."
docker run --rm -v "${PWD:-PWD must be set}"/pi-journal-new/journal:/journal ubuntu:latest sh -c "apt-get update -qq && apt-get install -y -qq systemd >/dev/null && journalctl -D /journal -xe --no-pager -u wpa_supplicant-wlan0 -u bedside"
