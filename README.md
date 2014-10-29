# Config Sync

Redis does not replicate configuration changes made via the `CONFIG SET`
command. Nor does Sentinel. This is a small tool which is meant to reside on a
sentinel to read it's config and synchronize all monitored slaves with their
master's for a list of configuration directives.

# Configuration

The only configuration currently available is to tell it where the Sentinel config file lives. By default it will look to `/etc/redis/sentinel.conf`, but this can be changed via the environment variable `CONFIGSYNC_SENTINELCONFIGFILE`.


# Anticipated Usage

Generally this is expected to be put into a cron table to run perodically. It
has been tested running against a sentinel with 100 monitored pods and runs in
under half a second, so should be safe to run every minute if desired.
