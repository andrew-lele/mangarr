let
  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
  inputs.unflake = {
    url = "git+https://codeberg.org/goldstein/unflake";
    flake = true;
  };
  _unflake.dedupRules = [ ];
in
inputs
