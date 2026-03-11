{
	lib,
	buildGoModule,
	...
}:

buildGoModule rec {
	pname = "pve-local-exporter";
	version = "0.1.0";
	src = ./src;
	vendorHash = "sha256-f0f8tYmoI6DtuB/K4++gu9b2na/d0ECTaF2zvDijW58=";
	ldflags = [
		"-X=main.version=${version}"
	];
	env.CGO_ENABLED = 0;
	meta.mainProgram = "pve_local_exporter";
}
