@echo off
set GO111MODULE=on
set PWD=%~dp0
for /F "tokens=1 delims= " %%i in ('echo %PWD:\go\src=\go \src%') do (set GOPATH=%%i)
for /F "tokens=2 delims=g" %%i in ('go version') do ( set GO_VERSION=g%%i )
for /F "tokens=* delims= " %%i in ('git rev-parse --short HEAD') do (set GIT_REVISION=%%i)
for /F "tokens=* delims= " %%i in ('git rev-parse --abbrev-ref HEAD') do (set GIT_BRANCH=%%i)
for /F "tokens=* delims= " %%i in ('git diff --quiet') do (set WIP=%%i)
for /F "tokens=* delims= " %%i in ('git describe --exact-match') do (set TAG=%%i)
for /F "tokens=* delims= " %%i in ('echo %TAG:v=%') do (set TAG=%%i)

if "%WIP%" == "" set WIP=WIP
if "%TAG%" == "" (
set IMAGE_TAG=%GIT_BRANCH%-%GIT_REVISION%-%WIP%
goto Next
)
set IMAGE_TAG=%TAG%

:Next
set BUILD_TIME=%date% %time%
set VPREFIX=github.com/grafana/loki/pkg/build
set GO_LDFLAGS=-X '%VPREFIX%.Branch=%GIT_BRANCH%' -X '%VPREFIX%.Version=%IMAGE_TAG%' -X '%VPREFIX%.Revision=%GIT_REVISION%' -X '%VPREFIX%.BuildUser=%GO_VERSION%' -X '%VPREFIX%.BuildDate=%BUILD_TIME%'
set GO_FLAGS=-ldflags "-extldflags \"-static\" -s -w %GO_LDFLAGS%" -tags netgo
set DEBUG_GO_FLAGS=-gcflags "all=-N -l" -ldflags "-extldflags \"-static\" %GO_LDFLAGS%" -tags netgo

go generate -x -v ./pkg/promtail/server/ui
go build %GO_FLAGS% -o ./dist/promtail/promtail.exe ./cmd/promtail
go build %DEBUG_GO_FLAGS% -o ./dist/promtail/promtail_debug.exe ./cmd/promtail
@echo on
