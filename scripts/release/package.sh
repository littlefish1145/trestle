#!/usr/bin/env bash
set -Eeuo pipefail

: "${GOOS:?GOOS is required}"
: "${GOARCH:?GOARCH is required}"
: "${VERSION:?VERSION is required}"
: "${COMMIT:?COMMIT is required}"
: "${BUILD_DATE:?BUILD_DATE is required}"

output_dir=${OUTPUT_DIR:-dist}
package_name="trestle-${VERSION}-${GOOS}-${GOARCH}"
stage_dir="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/${package_name}"
log_path="${output_dir}/${package_name}.build.log"

mkdir -p "$output_dir"
rm -rf "$stage_dir"
mkdir -p "$stage_dir/bin"

exec > >(tee "$log_path") 2>&1

binary_name=trestle
archive_path="${output_dir}/${package_name}.tar.gz"
if [[ "$GOOS" == windows ]]; then
  binary_name=trestle.exe
  archive_path="${output_dir}/${package_name}.zip"
fi

echo "Building $package_name"
go version
go env GOHOSTOS GOHOSTARCH
echo "target: ${GOOS}/${GOARCH}"
echo "commit: $COMMIT"
echo "build date: $BUILD_DATE"

CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
  -trimpath \
  -buildvcs=true \
  -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
  -o "$stage_dir/bin/$binary_name" \
  ./cmd/trestle

cp LICENSE README.md README_EN.md "$stage_dir/"
cp scripts/release/install.sh scripts/release/install.ps1 "$stage_dir/"
chmod 0755 "$stage_dir/install.sh"

cat > "$stage_dir/BUILD-INFO.txt" <<EOF
Trestle Alpha
Version: $VERSION
Commit: $COMMIT
Build date: $BUILD_DATE
Target: $GOOS/$GOARCH
CGO: disabled
EOF

cat > "${output_dir}/${package_name}.json" <<EOF
{"name":"trestle","version":"$VERSION","commit":"$COMMIT","build_date":"$BUILD_DATE","goos":"$GOOS","goarch":"$GOARCH","cgo":false,"archive":"$(basename "$archive_path")"}
EOF

if [[ "$GOOS" == windows ]]; then
  (
    cd "$(dirname "$stage_dir")"
    zip -q -r "$OLDPWD/$archive_path" "$package_name"
  )
else
  tar -C "$(dirname "$stage_dir")" -czf "$archive_path" "$package_name"
fi

echo "Created $archive_path"
