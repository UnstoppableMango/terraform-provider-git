{
  buildGoApplication,
  lib,
  ginkgo,
  git,
  version,
}:
buildGoApplication {
  pname = "terraform-provider-git";
  inherit version;

  src = lib.cleanSource ../.;
  modules = ./gomod2nix.toml;

  nativeCheckInputs = [
    ginkgo
    git
  ];

  checkPhase = ''
    ginkgo run ./...
  '';
}
