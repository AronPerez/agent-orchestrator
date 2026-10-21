#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
build=$(mktemp -d)
trap 'rm -rf "$build"' EXIT
tests=watch-model/Tests/BackgroundDelivery
for module in WatchConnectivity WidgetKit; do
    swiftc -emit-library -emit-module -module-name "$module" \
        "$tests/$module.swift" -o "$build/lib$module.dylib" -emit-module-path "$build/$module.swiftmodule"
done
swiftc -I "$build" -L "$build" -lWatchConnectivity -lWidgetKit \
    targets/watch/InboxStore.swift watch-model/Sources/WatchModel/WatchActionState.swift \
    "$tests/InboxStoreTests.swift" -o "$build/background-tests"
DYLD_LIBRARY_PATH="$build" "$build/background-tests"
