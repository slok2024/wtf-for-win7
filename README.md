# wtf for win7 #



set GOOS=windows

set GOARCH=386

set CGO_ENABLED=0

go build -ldflags="-s -w" -o wtfutil32.exe main.go



set GOOS=windows

set GOARCH=amd64

set CGO_ENABLED=0

go build -ldflags="-s -w" -o wtfutil64.exe main.go
