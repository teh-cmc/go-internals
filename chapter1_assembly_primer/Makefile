GOOS=linux
GOARCH=amd64

SOURCES := $(wildcard *.go)
OBJECTS = $(SOURCES:.go=.o)
EXECUTABLES = $(OBJECTS:.o=)

.SECONDARY: ${OBJECTS}

all: ${EXECUTABLES}

%.o: %.go
	GOOS=${GOOS} GOARCH=${GOARCH} go tool compile $<

%: %.o
	GOOS=${GOOS} GOARCH=${GOARCH} go tool link -o $@ $<

clean:
	rm ${OBJECTS}
	rm ${EXECUTABLES}
