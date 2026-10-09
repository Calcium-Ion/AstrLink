#!/bin/sh
# Container entrypoint. The container starts as root only to give the data
# directory to the astrlink user, so a fresh named volume or a bind-mounted
# host directory of any owner works without a manual chown. Core itself then
# runs as astrlink through the astrlink-core wrapper.
set -eu

data_dir=${ASTRLINK_DATA_DIR:-/data}

if [ "$(id -u)" = 0 ]; then
	mkdir -p "$data_dir"
	# Only entries owned by someone else change, so restarts stay quick.
	if ! find "$data_dir" \( ! -user astrlink -o ! -group astrlink \) -exec chown -h astrlink:astrlink {} +; then
		echo "astrlink: cannot give $data_dir to the astrlink user (uid $(id -u astrlink))." >&2
		echo "astrlink: give that uid write access to the directory, or mount a named volume instead." >&2
		exit 1
	fi
fi

exec astrlink-core "$@"
