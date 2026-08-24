{
  description = "A terraform provider for git";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    systems.url = "github:nix-systems/triplet";

    flake-parts = {
      url = "github:hercules-ci/flake-parts";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };

    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.inputs.systems.follows = "systems";
    };
  };

  outputs =
    inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = import inputs.systems;
      imports = with inputs; [ treefmt-nix.flakeModule ];

      perSystem =
        { pkgs, system, ... }:
        let
          version = pkgs.lib.strings.trim (builtins.readFile ./VERSION);
        in
        {
          _module.args.pkgs = import inputs.nixpkgs {
            inherit system;
            overlays = with inputs; [ gomod2nix.overlays.default ];
          };

          packages.default = pkgs.callPackage ./nix { inherit version; };

          devShells.default = pkgs.mkShellNoCC {
            packages = with pkgs; [
              direnv
              go
              gomod2nix
              gopls
              gnumake
              nixfmt
              golangci-lint
              opentofu
              sops
              terraform-plugin-docs
            ];

            TF_ACC_TERRAFORM_PATH = "${pkgs.opentofu}/bin/tofu";
            TF_ACC_PROVIDER_NAMESPACE = "hashicorp";
            TF_ACC_PROVIDER_HOST = "registry.opentofu.org";
          };

          treefmt.programs = {
            actionlint.enable = true;
            gofmt.enable = true;
            mdformat.enable = true;
            nixfmt.enable = true;
            shellcheck.enable = true;
          };

          treefmt.settings.formatter.mdformat.excludes = [
            ".agents/skills/**"
            ".claude/skills/**"
            "CHANGELOG.md"
            "docs/index.md"
            "docs/data-sources/**"
            "docs/resources/**"
            "docs/guides/**"
          ];
        };
    };
}
