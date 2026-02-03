Currently we refer to "user config" (~/.config/dev-mode/config.toml) a lot, but actually this is better explained as the daemon config.

- Update the default path to ~/.config/dev-mode/daemon.toml
- Update the code to use "daemon config" vocabulary instead of "user config"
- Update all docs to use "daemon config" vocabulary as well

Also make the location of this config file configurable via the env variable DEV_MODE_DAEMON_CONFIG
