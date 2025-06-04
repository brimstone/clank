VERSION = -X github.com/brimstone/ollamacli/version.version=dev-$(shell date +%Y-%m-%dT%H:%M:%S)
all: ollamacli ollamacli.exe

ollamacli: main.go */*.go Makefile
	go build -v -ldflags "-s -w ${VERSION}"

ollamacli.exe: main.go Makefile
	GOOS=windows go build -v -ldflags "-s -w ${VERSION}"
