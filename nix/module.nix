{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.audiorelay;

  command = [
    (lib.getExe cfg.package)
    "-input"
    cfg.input
    "-output"
    cfg.output
    "-bitrate"
    cfg.bitrate
    "-channels"
    (toString cfg.channels)
    "-sample-rate"
    (toString cfg.sampleRate)
    "-log-level"
    cfg.logLevel
    "-source-password-file"
  ];
in
{
  options.services.audiorelay = {
    enable = lib.mkEnableOption "the audiorelay live audio relay";

    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.callPackage ./package.nix { };
      defaultText = lib.literalExpression "pkgs.callPackage ./nix/package.nix { }";
      description = "The audiorelay package to run.";
    };

    input = lib.mkOption {
      type = lib.types.str;
      default = "";
      example = "https://upstream.example/live.m3u8";
      description = "Upstream media URL.";
    };

    output = lib.mkOption {
      type = lib.types.str;
      default = "";
      example = "icecast://source@[::1]:8000/tv.opus";
      description = ''
        Downstream sink URL. It must include the Icecast source username but
        must not include its password; use sourcePasswordFile for the password.
      '';
    };

    sourcePasswordFile = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "/run/secrets/audiorelay-source-password";
      description = ''
        Path to the file containing the Icecast source password. The module
        passes this path to systemd LoadCredential, so Nix does not read the
        secret or copy it to the Nix store. Use a string path from a secret
        manager such as sops-nix or agenix, not a Nix path literal.
      '';
    };

    bitrate = lib.mkOption {
      type = lib.types.str;
      default = "32k";
      description = "Target Opus bitrate.";
    };

    channels = lib.mkOption {
      type = lib.types.ints.between 1 2;
      default = 2;
      description = "Number of output audio channels.";
    };

    sampleRate = lib.mkOption {
      type = lib.types.enum [
        8000
        12000
        16000
        24000
        48000
      ];
      default = 48000;
      description = "Output audio sample rate in Hz.";
    };

    logLevel = lib.mkOption {
      type = lib.types.enum [
        "debug"
        "info"
        "warn"
        "error"
      ];
      default = "info";
      description = "Structured log verbosity.";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.input != "";
        message = "services.audiorelay.input must be set";
      }
      {
        assertion = cfg.output != "";
        message = "services.audiorelay.output must be set";
      }
      {
        assertion = cfg.sourcePasswordFile != null;
        message = "services.audiorelay.sourcePasswordFile must be set";
      }
    ];

    systemd.services.audiorelay = {
      description = "audiorelay live audio relay";
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      # The script keeps the credential directory expansion inside systemd's
      # service environment. The password itself is never in the generated
      # unit, process command line, Nix store, or application logs.
      script = ''
        exec ${lib.escapeShellArgs command} "$CREDENTIALS_DIRECTORY/source-password"
      '';

      serviceConfig = {
        DynamicUser = true;
        LoadCredential = lib.optional (
          cfg.sourcePasswordFile != null
        ) "source-password:${cfg.sourcePasswordFile}";
        # Transient failures are restarted, but permanent configuration or
        # upstream errors exit with EX_CONFIG (78) and must stay stopped.
        Restart = "on-failure";
        RestartPreventExitStatus = 78;
        RestartSec = "5s";

        NoNewPrivileges = true;
        PrivateDevices = true;
        PrivateTmp = true;
        ProtectHome = true;
        ProtectSystem = "strict";
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
          "AF_UNIX"
        ];
      };
    };
  };
}
