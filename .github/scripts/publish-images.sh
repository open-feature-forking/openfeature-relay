#!/usr/bin/env bash
# Pushes the image layouts of a run to ghcr.io/open-feature-forking/ (step 17). Only the publish-images and move-main jobs of
# build.yaml call it, on the main branch of this repository; no session runs it. The work is publish-images.py's: it holds
# the destinations and refuses any other.
#
# usage: publish-images.sh push <layouts directory> --short <7 hex> [--outputs <file>]
#        publish-images.sh move-main --short <7 hex> --sha <commit> [--force]
set -euo pipefail
exec python3 "$(dirname "$0")/publish-images.py" "$@"
