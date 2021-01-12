for /F "tokens=2 delims=g" %%i in ('go version') do ( set GO_VERSION=g%%i)
for /F "tokens=* delims= " %%i in ('git rev-parse --short HEAD') do ( set GIT_REVISION=%%i)
for /F "tokens=* delims= " %%i in ('git rev-parse --abbrev-ref HEAD') do ( set GIT_BRANCH=%%i)

set BUILD_TIME=%date% %time%
set VPREFIX=github.com/grafana/loki/pkg/build
set GO_LDFLAGS=-X '%VPREFIX%.Branch=%GIT_BRANCH%' -X '%VPREFIX%.Version=%IMAGE_TAG%' -X '%VPREFIX%.Revision=%GIT_REVISION%' -X '%VPREFIX%.BuildUser=%GO_VERSION%' -X '%VPREFIX%.BuildDate=%BUILD_TIME%'
set GO_FLAGS=-ldflags "-extldflags \"-static\" -s -w %GO_LDFLAGS%" -tags netgo
set DEBUG_GO_FLAGS=-gcflags "all=-N -l" -ldflags "-extldflags \"-static\" %GO_LDFLAGS%" -tags netgo

go generate -x -v ./pkg/promtail/server/ui
go build %GO_FLAGS% -o ./dist/promtail/promtail.exe ./cmd/promtail/main.go
go build %DEBUG_GO_FLAGS% -o ./dist/promtail/promtail_debug.exe ./cmd/promtail/main.go

