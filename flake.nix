{
	description = "Proxmox VE local metrics exporter for Prometheus";

	inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

	outputs = { self, nixpkgs }: let
		forAllSystems = nixpkgs.lib.genAttrs [
			"x86_64-linux"
			"aarch64-linux"
			"riscv64-linux"
		];
	in {
		packages = forAllSystems (system: let
			pkgs = nixpkgs.legacyPackages.${system};
		in rec {
			pve-local-exporter = pkgs.callPackage ./. {};
			default = pve-local-exporter;
		});

		checks = forAllSystems (system: let
			pkgs = nixpkgs.legacyPackages.${system};
			pkg = self.packages.${system}.pve-local-exporter;
		in {
			vet = pkgs.runCommand "go-vet" {
				nativeBuildInputs = [ pkgs.go ];
				inherit (pkg) src goModules;
				CGO_ENABLED = 0;
			} ''
				export HOME=$TMPDIR
				export GOPATH=$TMPDIR/go
				workdir=$TMPDIR/src
				mkdir -p $workdir
				cp -r $src/* $workdir/
				chmod -R u+w $workdir
				ln -s $goModules $workdir/vendor
				cd $workdir
				go vet -mod=vendor ./...
				touch $out
			'';
		});

		devShells = forAllSystems (system: let
			pkgs = nixpkgs.legacyPackages.${system};
		in {
			default = pkgs.mkShell {
				buildInputs = [ pkgs.go pkgs.gopls ];
			};
		});
	};
}
