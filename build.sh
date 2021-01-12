#!/bin/bash
export GO111MODULE=on

# Version info for binaries
IMAGE_TAG=$(./tag.sh)
GO_VERSION=$(go version | sed 's/go version//g')
GIT_REVISION=$(git rev-parse --short HEAD)
GIT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
# Build flags
VPREFIX="github.com/grafana/loki/pkg/build"
GO_LDFLAGS="-X '$VPREFIX.Branch=$GIT_BRANCH' -X '$VPREFIX.Version=$IMAGE_TAG' -X '$VPREFIX.Revision=$GIT_REVISION' -X '$VPREFIX.BuildUser=$GO_VERSION' -X '$VPREFIX.BuildDate=$(date -u +"%Y-%m-%dT%H:%M:%SZ")'"

############
# Promtail #
############

PROMTAIL_CG=0

# Validate GOHOSTOS=linux && GOOS=linux to use CGO.
if [ $(go env GOHOSTOS) = 'linux' ]; then
  if [ $(go env GOOS) = 'linux' ]; then
    PROMTAIL_CGO=1
  fi
fi

# Per some websites I've seen to add `-gcflags "all=-N -l"`, the gcflags seem poorly if at all documented
# the best I could dig up is -N disables optimizations and -l disables inlining which should make debugging match source better.
# Also remove the -s and -w flags present in the normal build which strip the symbol table and the DWARF symbol table.
GOOS=$(go env GOHOSTOS) go generate -x -v ./pkg/promtail/server/ui
CGO_ENABLED=$PROMTAIL_CGO go build -ldflags "-s -w $GO_LDFLAGS" -tags netgo -o ./dist/promtail/promtail ./cmd/promtail
CGO_ENABLED=$PROMTAIL_CGO go build -gcflags "all=-N -l" -ldflags "$GO_LDFLAGS" -tags netgo -o ./dist/promtail/promtail_debug ./cmd/promtail

