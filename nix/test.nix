# SPDX-License-Identifier: AGPL-3.0-or-later
self:
{ lib, ... }:
{
  name = "skaldi";

  nodes.machine = { ... }: {
    imports = [ self.nixosModules.default ];

    services.skaldi = {
      enable = true;
      settings.server.port = 8080;
    };
    services.avahi.enable = true;

    # Give mpv an ALSA device so it starts cleanly in the headless VM.
    boot.kernelModules = [ "snd-dummy" ];
  };

  testScript = ''
    machine.wait_for_unit("skaldi.service")
    machine.wait_for_open_port(8080)
    machine.succeed("curl -sf http://localhost:8080/ | grep -qi skaldi")
  '';
}
