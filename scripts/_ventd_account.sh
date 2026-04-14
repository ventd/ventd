# shellcheck shell=sh
#
# Shared helper sourced by scripts/install.sh and scripts/postinstall.sh.
# Creates the "ventd" unprivileged system user and group if they do not
# already exist.
#
# POSIX sh (/bin/sh) ONLY. Debian's dash is the floor. Do not introduce
# bash-isms: no arrays, no [[ ]], no $(<file), no "local" keyword.
#
# Public entry point: ventd_create_account

ventd_create_account() {
    if ! getent group ventd >/dev/null 2>&1; then
        if command -v groupadd >/dev/null 2>&1; then
            groupadd --system ventd
        elif command -v addgroup >/dev/null 2>&1; then
            # BusyBox addgroup (Alpine, Void-musl).
            addgroup -S ventd
        else
            echo "error: neither groupadd nor addgroup available — cannot create ventd group" >&2
            return 1
        fi
    fi

    if ! getent passwd ventd >/dev/null 2>&1; then
        if command -v useradd >/dev/null 2>&1; then
            useradd --system --gid ventd --no-create-home \
                    --home-dir /nonexistent \
                    --shell /usr/sbin/nologin \
                    --comment "ventd fan control daemon" ventd
        elif command -v adduser >/dev/null 2>&1; then
            # BusyBox adduser.
            adduser -S -D -H -G ventd -s /sbin/nologin \
                    -g "ventd fan control daemon" ventd
        else
            echo "error: neither useradd nor adduser available — cannot create ventd user" >&2
            return 1
        fi
    fi
}
