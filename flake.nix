{
  description = "YOLO Jail: A restricted Docker environment for AI agents";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # Derivation for the shim scripts
        shims = pkgs.stdenv.mkDerivation {
          name = "yolo-shims";
          src = ./src/shims;
          installPhase = ''
            mkdir -p $out/bin
            cp * $out/bin/
            chmod +x $out/bin/*
          '';
        };

        # The Docker Image
        dockerImage = pkgs.dockerTools.buildLayeredImage {
          name = "yolo-jail";
          tag = "latest";
          created = "now";
          
          contents = [
            shims
            pkgs.bashInteractive
            pkgs.coreutils-full  # basic file manip
            pkgs.git
            pkgs.ripgrep
            pkgs.fd
            pkgs.curl
            pkgs.cacert
            pkgs.mise      # Tool manager
            pkgs.nodejs_22 # Use a specific stable version
            pkgs.python3   # Bootstrap for mise plugins
            pkgs.gh        # GitHub CLI
            pkgs.bashInteractive
            pkgs.coreutils-full
            pkgs.gnused
            pkgs.gnugrep   # We will shim these later, but we need the originals for some scripts
            pkgs.findutils
          ];

          config = {
            Cmd = [ "${pkgs.bashInteractive}/bin/bash" ];
            # We explicitly place shims first in PATH, though they shouldn't conflict if the others are missing.
            # But if bash pulls them in as deps, this ensures shims win.
            Env = [ 
              "PATH=/bin:${shims}/bin:/usr/bin" 
              "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
            ];
            WorkingDir = "/workspace";
          };
        };

      in
      {
        packages.default = dockerImage;
        packages.dockerImage = dockerImage;

        devShells.default = pkgs.mkShell {
          buildInputs = [
            pkgs.just
          ];
        };
      }
    );
}
