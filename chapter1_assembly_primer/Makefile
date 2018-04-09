GOOS=linux
GOARCH=amd64

SOURCES := $(wildcard *.go)
OBJECTS = $(SOURCES:.go=.o)
EXECUTABLES = $(OBJECTS:.o=.bin)

.SECONDARY: ${OBJECTS}

all: ${EXECUTABLES}

%.o: %.go
	GOOS=${GOOS} GOARCH=${GOARCH} go tool compile $<

%.bin: %.o
	GOOS=${GOOS} GOARCH=${GOARCH} go tool link -o $@ $<

clean:
	rm -f ${OBJECTS}
	rm -f ${EXECUTABLES}
