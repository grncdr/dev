{
  description = "dev - local dev environment manager";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      system = "aarch64-darwin";
      pkgs = import nixpkgs { inherit system; };
    in {
      packages.${system}.default = pkgs.buildGoModule {
        pname = "dev";
        version = "0.0.1";
        src = ./.;
        vendorHash = "sha256-23sA8v9vZxzyAczoq/OH+eyvJWiTV956HC+MxHH6g54=";
        doCheck = false;
      };
    };
}
