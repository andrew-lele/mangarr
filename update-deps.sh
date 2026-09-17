#!/usr/bin/env bash
# Wrapper script to run unflake with flakes enabled

export NIX_CONFIG="experimental-features = nix-command flakes"
nix-shell https://ln-s.sh/unflake -A unflake-shell --run "unflake $@"
