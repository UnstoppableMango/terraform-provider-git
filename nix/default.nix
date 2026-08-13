{
  buildGoApplication,
  lib,
  ginkgo,
  version,
}:
buildGoApplication {
  pname = "";
  inherit version;

  src = lib.cleanSource ../.;
  modules = ./gomod2nix.toml;

  nativeCheckInputs = [ ginkgo ];

  checkPhase = ''
    ginkgo run ./...
  '';
}
