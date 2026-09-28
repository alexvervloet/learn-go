#!/usr/bin/env bash
#
# Regenerates the Go stubs from the .proto files.
#
# The generated code is COMMITTED, deliberately: see the reasoning in the repo's .gitignore. This script is how
# you regenerate it, and CI runs it and diffs the result so a stale stub cannot go unnoticed.
#
# Needs:
#   protoc                  brew install protobuf   (the version in .protoc-version)
#   protoc-gen-go           go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   protoc-gen-go-grpc      go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
#
# The two plugins are separate binaries and separate modules, which is a 2020 split people still trip over:
# protoc-gen-go stopped generating gRPC service code, so a project using only it gets messages and no client.

set -euo pipefail

cd "$(dirname "$0")"

# The protoc version is pinned, because the generated files record it.
#
# Every .pb.go carries a `// protoc vX.Y.Z` line in its header. Two machines with different protoc builds
# produce byte-different output from identical .proto files, so CI's regenerate-and-diff job fails on a version
# mismatch and says nothing about the schema. That is what happened here: the workflow installed Ubuntu's
# protobuf-compiler (3.21.12) while this machine had 36.2, and the only diff in 8 files was that comment.
#
# The fix is to pin, in one place both CI and a developer read. A warning rather than an error, because being
# unable to regenerate at all is worse than a one-line diff you can see in `git diff`.
PINNED="$(cat .protoc-version)"
ACTUAL="$(protoc --version | awk '{print $2}')"

if [[ "$ACTUAL" != "$PINNED" ]]; then
  echo "warning: protoc $ACTUAL, but .protoc-version pins $PINNED." >&2
  echo "         The generated headers will differ and CI will flag it. Install $PINNED from" >&2
  echo "         https://github.com/protocolbuffers/protobuf/releases/tag/v$PINNED" >&2
fi

# --go_opt=module= rather than paths=source_relative.
#
# The go_package option in each .proto names a full import path. `module=` tells protoc to strip that prefix and
# write the file at the remaining path, so gen/greeterv1/greeter.pb.go lands where the import path says it is.
#
# paths=source_relative would put it next to the .proto instead, which means the generated package lives in
# proto/ alongside files that are not Go. Both work; this one keeps the Go tree and the schema tree separate.
MODULE=github.com/alexvervloet/learn-go/backends/learning/grpc-concepts

mkdir -p gen

protoc \
  --proto_path=proto \
  --go_out=. \
  --go_opt=module="$MODULE" \
  --go-grpc_out=. \
  --go-grpc_opt=module="$MODULE" \
  proto/*.proto

echo "generated:"
find gen -name '*.go' | sort | sed 's/^/  /'
