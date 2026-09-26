{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/master";
    make-shell.url = "github:nicknovitski/make-shell";
  };

  outputs = inputs@{ self, nixpkgs, flake-parts, systems, make-shell, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      imports = [ make-shell.flakeModules.default ];
      systems = [ "x86_64-linux" "aarch64-darwin" ];

      perSystem = { config, self', inputs', pkgs, system, ... }:
        let
          skew = pkgs.buildGoModule {
            pname = "skew";
            version = "0.1.0";
            src = self;
            vendorHash = "sha256-bHRh5mtlgeNLFyCEtYkGcBTRTGXSOboPg+0dx6yhGCM=";
          };
        in {
          packages = {
            inherit skew;
            default = skew;
          };

          make-shells.default = {
            packages = [
              skew
              pkgs.go
              pkgs.gopls
              pkgs.golangci-lint
              pkgs.golangci-lint-langserver
            ];
          };
        };
    };
}
