{
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  outputs = { self, nixpkgs }: {
    defaultPackage.aarch64-linux = nixpkgs.legacyPackages.aarch64-linux.buildEnv {
      name = "wormhole-env";
      paths = with nixpkgs.legacyPackages.aarch64-linux; [
        go
        gotools
        golangci-lint
      ];
      pathsToLink = [ "/" ];
      #extraOutputsToInstall = [ "man" "doc" ];
    };
  };
}
