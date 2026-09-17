# Dev shell for the mangarr fork (github.com/andrew-lele/mangarr).
#
# Uses the pinned nixpkgs from the infra repo (unflake) so the toolchain is
# reproducible. NOTE: the pinned nixpkgs has `go` (1.26.x) and `gopls` but does
# NOT package the golang v1 toolchain (`gofmt`, `go vet`, `go generate` are
# missing). Those tools are part of the upstream golang distribution
# (ci.Dockerfile FROM golang:1.27.0-alpine3.23). `go generate` additionally
# needs protoc + protoc-gen-go. For editing/LSP this shell is sufficient; run
# the generation/formatting gates in the CI image or a golang container.
#
# Usage: nix-shell
let
  inputs = import ./unflake.nix;
  pkgs = import inputs.nixpkgs { system = builtins.currentSystem; };
  pi-shell = import ../inferencer6000/pi-shell.nix;
  
in
pkgs.mkShell {
  name = "mangarr-dev";

  packages = [
    pi-shell.pi
    pkgs.go
    pkgs.gopls
    pkgs.git
    pkgs.secretspec
  ];

  shellHook = pi-shell.shellHook + ''
    # View logs from most recent execution of any systemd service
    # Usage: jlast bulwark.service
    jlast() {
      journalctl -u "$1" _SYSTEMD_INVOCATION_ID=$(systemctl show -p InvocationID --value "$1")
    }
    tl() {
      nix-build test-web.nix -A landing && python3 -m http.server 3016 -d result
    }
    tf() {
      nix-build test-web.nix -A food && python3 -m http.server 3016 -d result
    }
    if command -v go >/dev/null; then
      echo "go:  $(go version 2>/dev/null | head -1)"
    fi
    if command -v gopls >/dev/null; then
      echo "gopls: $(gopls --version 2>/dev/null)"
    fi
  '';
}
