{
  lib,
  buildGoModule,
  makeWrapper,
  mpv,
  ffmpeg,
  yt-dlp,
  bun,
  avahi,
}:

buildGoModule {
  pname = "skaldi";
  version = "0.1.0";

  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../cmd
      ../internal
      ../web
      ../go.mod
    ];
  };

  vendorHash = null;

  subPackages = [ "cmd/skaldi" ];
  ldflags = [ "-s" "-w" ];

  nativeBuildInputs = [ makeWrapper ];

  postInstall = ''
    wrapProgram $out/bin/skaldi \
      --prefix PATH : ${lib.makeBinPath [ mpv ffmpeg yt-dlp bun avahi ]} \
      --set-default SKALDI_PROVISION 0
  '';

  meta = {
    description = "Self-hosted network jukebox: one Go binary, embedded web UI";
    homepage = "https://github.com/reuski/skaldi";
    license = lib.licenses.agpl3Plus;
    mainProgram = "skaldi";
    platforms = lib.platforms.unix;
  };
}
