{
	lib,
	buildGoModule,
	...
}:

buildGoModule rec {
	pname = "pve-local-exporter";
	version = "0.1.0";
	src = ./src;
	vendorHash = "sha256-MLB7y7shnOhxW8K2R6+d9E63wGEhlErnv+1MYOJO3Hw=";
	ldflags = [
		"-X=main.version=${version}"
	];
	env.CGO_ENABLED = 0;
	meta.mainProgram = "pve_local_exporter";
}
