#!/bin/sh
# Run the full test suite including the OPDS 1.2 RELAX NG conformance tests,
# which require Jing (a Java RELAX NG validator). Jing is downloaded into
# tools/ on first run. The OPDS 2.0 JSON Schema conformance tests are pure Go
# and always run.
#
# Usage: scripts/conformance.sh [extra `go test` args]

set -e
cd "$(dirname "$0")/.."

JING_VERSION=20241231
TOOLS=tools

if ! command -v java >/dev/null 2>&1; then
	echo "error: java is required for OPDS 1.2 RELAX NG conformance" >&2
	exit 1
fi

JING=$(find "$TOOLS" -name jing.jar 2>/dev/null | head -1 || true)
if [ -z "$JING" ]; then
	echo "Downloading Jing $JING_VERSION..."
	mkdir -p "$TOOLS"
	url="https://github.com/relaxng/jing-trang/releases/download/V$JING_VERSION/jing-$JING_VERSION.zip"
	curl -sSL "$url" -o "$TOOLS/jing.zip"
	if command -v unzip >/dev/null 2>&1; then
		(cd "$TOOLS" && unzip -oq jing.zip)
	else
		(cd "$TOOLS" && jar xf jing.zip)
	fi
	JING=$(find "$TOOLS" -name jing.jar | head -1)
fi

OPDS_JING_JAR="$PWD/$JING"
export OPDS_JING_JAR
echo "Using Jing at $OPDS_JING_JAR"
go test ./... "$@"
