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

		devShells = forAllSystems (system: let
			pkgs = nixpkgs.legacyPackages.${system};
		in {
			default = pkgs.mkShell {
				buildInputs = [ pkgs.go pkgs.gopls ];
			};
		});
	};
}
