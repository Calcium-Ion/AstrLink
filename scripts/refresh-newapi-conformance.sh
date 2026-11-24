#!/bin/sh
# Copies new-api's official plugins and the top-level meta fields of its
# plugin API v1 schema into Core's conformance testdata.
#
#   scripts/refresh-newapi-conformance.sh <new-api checkout>
set -eu

if [ "$#" -ne 1 ] || [ ! -f "$1/docs/plugin-api/v1.schema.json" ] || [ ! -d "$1/plugins/tasks" ]; then
	echo "usage: $0 <new-api checkout>" >&2
	exit 2
fi
source_dir=$1
target=$(cd "$(dirname "$0")/.." && pwd)/core/internal/plugin/testdata/newapi-conformance

commit=$(git -C "$source_dir" rev-parse HEAD)
rm -rf "$target/plugins"
mkdir -p "$target/plugins"
for dir in "$source_dir"/plugins/tasks/*/; do
	key=$(basename "$dir")
	[ -f "$dir/plugin.js" ] || continue
	mkdir -p "$target/plugins/$key"
	cp "$dir/plugin.js" "$target/plugins/$key/plugin.js"
done
python3 - "$source_dir/docs/plugin-api/v1.schema.json" "$target/meta-fields.json" <<'PY'
import json, sys
with open(sys.argv[1]) as schema:
    fields = sorted(json.load(schema)["properties"])
with open(sys.argv[2], "w") as out:
    json.dump(fields, out, indent=2)
    out.write("\n")
PY
cat > "$target/SOURCE" <<TXT
Copied from https://github.com/QuantumNous/new-api at commit $commit:
plugins/tasks/*/plugin.js into plugins/, and the top-level property names
of docs/plugin-api/v1.schema.json into meta-fields.json. new-api is
licensed under AGPL-3.0 (LICENSES/AGPL-3.0.txt). Refresh with
scripts/refresh-newapi-conformance.sh <new-api checkout>.
TXT
echo "copied $(ls "$target/plugins" | wc -l | tr -d ' ') plugins from $commit"
