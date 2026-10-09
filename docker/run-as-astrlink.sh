#!/bin/sh
# Installed as both `astrlink` and `astrlink-core`. It runs the binary of that
# name as the astrlink user, also when `docker exec` enters as root: Core's
# control socket only accepts the user Core runs as, and files written under
# the data directory stay owned by that user.
set -eu

case $(basename "$0") in
astrlink) binary=/opt/astrlink/astrlink-cli ;;
astrlink-core) binary=/opt/astrlink/astrlink-core ;;
*)
	echo "run-as-astrlink: install this script as astrlink or astrlink-core" >&2
	exit 64
	;;
esac

if [ "$(id -u)" = 0 ]; then
	export HOME=/home/astrlink
	exec setpriv --reuid=astrlink --regid=astrlink --init-groups --no-new-privs --inh-caps=-all "$binary" "$@"
fi
exec "$binary" "$@"
