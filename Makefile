.PHONY: toc

toc:
	docker run --rm -it -v ${PWD}:/usr/src jorgeandrada/doctoc --github
	$(shell tail -n +`grep -n '# \`go-internals\`' README.md | tr ':' ' ' | awk '{print $$1}'` README.md > /tmp/README2.md)
	cp /tmp/README2.md README.md
