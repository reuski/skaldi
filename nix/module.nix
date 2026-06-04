# SPDX-License-Identifier: AGPL-3.0-or-later
self:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.skaldi;
  settingsFormat = pkgs.formats.json { };
  configFile = settingsFormat.generate "skaldi-config.json" cfg.settings;
in
{
  options.services.skaldi = {
    enable = lib.mkEnableOption "Skaldi network jukebox";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.skaldi;
      defaultText = lib.literalExpression "skaldi.packages.\${system}.skaldi";
      description = "The Skaldi package to run (self-contained, wrapped with mpv/ffmpeg/yt-dlp/bun/avahi).";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Open the HTTP port and mDNS (UDP 5353) in the firewall.";
    };

    settings = lib.mkOption {
      type = settingsFormat.type;
      default = { };
      example = lib.literalExpression ''
        {
          server.port = 8080;
          opensubsonic = {
            enabled = true;
            library_id = "personal";
            base_url = "https://navidrome.example.com";
            username = "alice";
            token = "server_token_secret";
          };
        }
      '';
      description = ''
        Skaldi configuration, rendered to config.json. `provision` is forced off
        because the package supplies yt-dlp/bun from the store.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    services.skaldi.settings = {
      provision = lib.mkForce false;
      server.port = lib.mkDefault 8080;
    };

    warnings = lib.optional (!config.services.avahi.enable)
      "services.skaldi: mDNS (skaldi.local) needs services.avahi.enable = true.";

    networking.firewall = lib.mkIf cfg.openFirewall {
      allowedTCPPorts = [ cfg.settings.server.port ];
      allowedUDPPorts = [ 5353 ];
    };

    systemd.services.skaldi = {
      description = "Skaldi network jukebox";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [ "network-online.target" "sound.target" ];

      environment = {
        SKALDI_CONFIG = builtins.toString configFile;
        XDG_CACHE_HOME = "%C"; # /var/cache  -> cache/skaldi
        XDG_DATA_HOME = "%S"; # /var/lib    -> lib/skaldi/history
        HOME = "%S";
      };

      serviceConfig = {
        ExecStart = lib.getExe cfg.package;
        Restart = "on-failure";
        RestartSec = 2;

        DynamicUser = true;
        StateDirectory = "skaldi";
        CacheDirectory = "skaldi";
        RuntimeDirectory = "skaldi";

        # Audio: a headless server still needs an output device. ALSA access via
        # the `audio` group covers the common case; PipeWire/Pulse setups may
        # need extra wiring (see README).
        SupplementaryGroups = [ "audio" ];

        # Hardening.
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        ProtectClock = true;
        ProtectHostname = true;
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = false; # bun/yt-dlp JIT needs W^X relaxed.
        RestrictAddressFamilies = [ "AF_UNIX" "AF_INET" "AF_INET6" "AF_NETLINK" ];
        SystemCallFilter = [ "@system-service" ];
        SystemCallErrorNumber = "EPERM";
      };
    };
  };
}
