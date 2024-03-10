{
  #inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  inputs.nixpkgs.url = "https://flakehub.com/f/NixOS/nixpkgs/0.1.*.tar.gz";
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

