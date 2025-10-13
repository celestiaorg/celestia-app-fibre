#!/usr/bin/env bash

set -e

# Generate protos from proto/ directory (centralized location)
cd proto
proto_dirs=$(find . -name '*.proto' -print0 | xargs -0 -n1 dirname | sort | uniq)
for dir in $proto_dirs; do
  for file in $(find "${dir}" -maxdepth 1 -name '*.proto'); do
      echo "Generating gogo proto code for ${file}"
      buf generate --template buf.gen.gogo.yaml $file
  done
done

cd ..

# move proto files to the right places
cp -r github.com/celestiaorg/celestia-app/* ./
rm -rf github.com

# Generate protos from x/ directories (per-package location)
for proto_dir in $(find x -type d -name proto); do
  if [ -f "$proto_dir/buf.gen.yaml" ]; then
    echo "Generating proto code in $proto_dir"
    for file in $(find $proto_dir -maxdepth 1 -name '*.proto'); do
      echo "Generating proto code for ${file}"
      buf generate --template $proto_dir/buf.gen.yaml --path $file
    done
  fi
done
