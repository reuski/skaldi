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

    boot.kernelModules = [ "snd-dummy" ];
  };

  testScript = ''
    machine.wait_for_unit("skaldi.service")
    machine.wait_for_open_port(8080)
    machine.succeed("curl -fsS -o /tmp/skaldi-index.html http://localhost:8080/")
    machine.succeed("grep -qi skaldi /tmp/skaldi-index.html")
  '';
}
