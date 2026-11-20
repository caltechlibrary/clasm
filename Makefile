
# generated with CMTools 0.0.0 7c42249

#
# Simple Makefile for Golang based Projects built under POSIX.
#
PROJECT = clasm

GIT_GROUP = caltechlibrary

PROGRAMS = clasm clmpm

RELEASE_DATE = $(shell date +%Y-%m-%d)

RELEASE_HASH=$(shell git log --pretty=format:'%h' -n 1)

MAN_PAGES_1 = $(shell ls -1 docs/*.1.md | sed -E 's|docs/||; s/.1.md/.1/g')

#MAN_PAGES_3 = $(shell ls -1 *.3.md | sed -E 's/.3.md/.3/g')

#MAN_PAGES_7 = $(shell ls -1 *.7.md | sed -E 's/.7.md/.7/g')

DOCS = $(shell ls -1 docs/*.?.md)

# version.go is left out: it has a rule (cmt, under generate), and listing it
# here would make every build run cmt through that rule.
PACKAGE = $(filter-out version.go,$(shell ls -1 *.go))

VERSION = $(shell grep '"version":' codemeta.json | cut -d\"  -f 4)

BRANCH = $(shell git branch | grep '* ' | cut -d  -f 2)

OS = $(shell uname)

#PREFIX = /usr/local/bin
PREFIX = $(HOME)

ifneq ($(prefix),)
	PREFIX = $(prefix)
endif

EXT =
ifeq ($(OS), Windows)
	EXT = .exe
endif

# build compiles the program and regenerates the help text and man pages. It
# does not run cmt: version.go, CITATION.cff and the installers are generated
# from codemeta.json by `make generate`, so the version and release hash change
# only when that is asked for (the release target does), and `make; make
# install` leaves the committed files alone.
build: $(PROGRAMS) man

# generate runs cmt over codemeta.json. Run it when the version, the release
# notes or the metadata change, and before a release; a plain `make` leaves the
# committed version.go, CITATION.cff and installers alone.
generate: version.go CITATION.cff installer.sh installer.ps1

version.go: .FORCE
	cmt codemeta.json version.go

hash: .FORCE
	git log --pretty=format:'%h' -n 1

man: $(MAN_PAGES_1) # $(MAN_PAGES_3) $(MAN_PAGES_7)

$(MAN_PAGES_1): .FORCE
	mkdir -p man/man1
	pandoc docs/$@.md --from markdown --to man -s >man/man1/$@

$(MAN_PAGES_3): .FORCE
	mkdir -p man/man3
	pandoc docs/$@.md --from markdown --to man -s >man/man3/$@

$(MAN_PAGES_7): .FORCE
	mkdir -p man/man7
	pandoc docs/$@.md --from markdown --to man -s >man/man7/$@

$(PROGRAMS): $(PACKAGE)
	@mkdir -p bin
	go build -o "bin/$@$(EXT)" cmd/$@/*.go
	@./bin/$@ -help >docs/$@.1.md

$(MAN_PAGES): .FORCE
	mkdir -p man/man1
	pandoc docs/$@.md --from markdown --to man -s >man/man1/$@

CITATION.cff: .FORCE
	cmt codemeta.json CITATION.cff

installer.sh: .FORCE
	cmt codemeta.json installer.sh

installer.ps1: .FORCE
	cmt codemeta.json installer.ps1


test: $(PACKAGE)
	go test

status:
	git status

save:
	@if [ "$(msg)" != "" ]; then git commit -am "$(msg)"; else git commit -am "Quick Save"; fi
	git push origin $(BRANCH)

refresh:
	git fetch origin
	git pull origin $(BRANCH)

clean:
	@if [ -f version.go ]; then rm version.go; fi
	@if [ -d bin ]; then rm -fR bin; fi
	@if [ -d dist ]; then rm -fR dist; fi
	@if [ -d man ]; then rm -fR man; fi
	@if [ -d testout ]; then rm -fR testout; fi

install: build
	@echo "Installing programs in $(PREFIX)/bin"
	@for FNAME in $(PROGRAMS); do if [ -f "./bin/$${FNAME}$(EXT)" ]; then mv -v "./bin/$${FNAME}$(EXT)" "$(PREFIX)/bin/$${FNAME}$(EXT)"; fi; done
	@echo ""
	@echo "Make sure $(PREFIX)/bin is in your PATH"
	@echo "Installing man page in $(PREFIX)/man"
	@mkdir -p $(PREFIX)/man/man1
	@for FNAME in $(MAN_PAGES_1); do if [ -f "./man/man1/$${FNAME}" ]; then cp -v "./man/man1/$${FNAME}" "$(PREFIX)/man/man1/$${FNAME}"; fi; done
	@mkdir -p $(PREFIX)/man/man3
	@for FNAME in $(MAN_PAGES_3); do if [ -f "./man/man3/$${FNAME}" ]; then cp -v "./man/man3/$${FNAME}" "$(PREFIX)/man/man3/$${FNAME}"; fi; done
	@mkdir -p $(PREFIX)/man/man7
	@for FNAME in $(MAN_PAGES_7); do if [ -f "./man/man7/$${FNAME}" ]; then cp -v "./man/man7/$${FNAME}" "$(PREFIX)/man/man7/$${FNAME}"; fi; done
	@echo ""
	@echo "Make sure $(PREFIX)/man is in your MANPATH"

uninstall: .FORCE
	@echo "Removing programs in $(PREFIX)/bin"
	@for FNAME in $(PROGRAMS); do if [ -f "$(PREFIX)/bin/$${FNAME}$(EXT)" ]; then rm -v "$(PREFIX)/bin/$${FNAME}$(EXT)"; fi; done
	@echo "Removing man pages in $(PREFIX)/man"
	@for FNAME in $(MAN_PAGES_1); do if [ -f "$(PREFIX)/man/man1/$${FNAME}" ]; then rm -v "$(PREFIX)/man/man1/$${FNAME}"; fi; done
	@for FNAME in $(MAN_PAGES_3); do if [ -f "$(PREFIX)/man/man3/$${FNAME}" ]; then rm -v "$(PREFIX)/man/man3/$${FNAME}"; fi; done
	@for FNAME in $(MAN_PAGES_7); do if [ -f "$(PREFIX)/man/man7/$${FNAME}" ]; then rm -v "$(PREFIX)/man/man7/$${FNAME}"; fi; done

setup_dist: .FORCE
	@mkdir -p dist
	@rm -fR dist/

dist/Linux-x86_64: $(PROGRAMS)
	@mkdir -p dist/bin
	@for FNAME in $(PROGRAMS); do env  GOOS=linux GOARCH=amd64 go build -o "dist/bin/$${FNAME}" cmd/$${FNAME}/*.go; done
	@cd dist && zip -r $(PROJECT)-v$(VERSION)-Linux-x86_64.zip LICENSE codemeta.json CITATION.cff *.md bin/* man/*
	@rm -fR dist/bin

dist/Linux-aarch64: $(PROGRAMS)
	@mkdir -p dist/bin
	@for FNAME in $(PROGRAMS); do env  GOOS=linux GOARCH=arm64 go build -o "dist/bin/$${FNAME}" cmd/$${FNAME}/*.go; done
	@cd dist && zip -r $(PROJECT)-v$(VERSION)-Linux-aarch64.zip LICENSE codemeta.json CITATION.cff *.md bin/* man/*
	@rm -fR dist/bin

dist/macOS-x86_64: $(PROGRAMS)
	@mkdir -p dist/bin
	@for FNAME in $(PROGRAMS); do env GOOS=darwin GOARCH=amd64 go build -o "dist/bin/$${FNAME}" cmd/$${FNAME}/*.go; done
	@cd dist && zip -r $(PROJECT)-v$(VERSION)-macOS-x86_64.zip LICENSE codemeta.json CITATION.cff *.md bin/* man/*
	@rm -fR dist/bin


dist/macOS-arm64: $(PROGRAMS)
	@mkdir -p dist/bin
	@for FNAME in $(PROGRAMS); do env GOOS=darwin GOARCH=arm64 go build -o "dist/bin/$${FNAME}" cmd/$${FNAME}/*.go; done
	@cd dist && zip -r $(PROJECT)-v$(VERSION)-macOS-arm64.zip LICENSE codemeta.json CITATION.cff *.md bin/* man/*
	@rm -fR dist/bin


dist/Windows-x86_64: $(PROGRAMS)
	@mkdir -p dist/bin
	@for FNAME in $(PROGRAMS); do env GOOS=windows GOARCH=amd64 go build -o "dist/bin/$${FNAME}.exe" cmd/$${FNAME}/*.go; done
	@cd dist && zip -r $(PROJECT)-v$(VERSION)-Windows-x86_64.zip LICENSE codemeta.json CITATION.cff *.md bin/* man/*
	@rm -fR dist/bin

dist/Windows-arm64: $(PROGRAMS)
	@mkdir -p dist/bin
	@for FNAME in $(PROGRAMS); do env GOOS=windows GOARCH=arm64 go build -o "dist/bin/$${FNAME}.exe" cmd/$${FNAME}/*.go; done
	@cd dist && zip -r $(PROJECT)-v$(VERSION)-Windows-arm64.zip LICENSE codemeta.json CITATION.cff *.md bin/* man/*
	@rm -fR dist/bin

distribute_docs:
	@mkdir -p dist/
	@cp -v codemeta.json dist/
	@cp -v CITATION.cff dist/
	@cp -v README.md dist/
	@cp -v LICENSE dist/
	@cp -v INSTALL.md dist/
	@mkdir -p dist/man/man1
	@for FNAME in $(MAN_PAGES_1); do cp -v "man/man1/$${FNAME}" dist/man/man1/; done
	@for DNAME in $(DOCS); do cp -v $$DNAME dist/; done

# test-release runs the tests of release.bash, and of release.ps1 when pwsh is
# installed. They use throwaway repositories and a stub gh, so nothing reaches
# GitHub. Run it after changing release.bash, release.ps1 or release_test.bash.
test-release: .FORCE
	./release_test.bash
	@if command -v pwsh >/dev/null 2>&1; then RELEASE_IMPL=ps1 ./release_test.bash; else echo "pwsh not found: release.ps1 not tested"; fi

# check-release stops a release that is not ready. It changes nothing. It checks
# that codemeta.json, version.go, CITATION.cff, both installers and the manual
# pages in docs/ all carry the version in codemeta.json, that the release notes
# begin with that version, that dateModified and datePublished agree, and then
# runs go vet and the whole test suite (the live tests are opt-in by environment
# variable and are skipped). It does not compare the release hash, which
# `make generate` rewrites; the release target runs generate first. Run
# `make generate build` before running it by hand on a changed codemeta.json.
check-release: .FORCE
	@if [ -z "$(VERSION)" ]; then \
		echo "error: no version in codemeta.json" >&2; exit 1; fi; \
	if ! jq -r .releaseNotes codemeta.json | grep -q '^v$(VERSION) '; then \
		echo "error: codemeta.json releaseNotes do not begin with v$(VERSION)" >&2; exit 1; fi; \
	if [ "$$(jq -r .dateModified codemeta.json)" != "$$(jq -r .datePublished codemeta.json)" ]; then \
		echo "error: codemeta.json dateModified and datePublished differ" >&2; exit 1; fi; \
	if ! grep -q 'Version = "$(VERSION)"' version.go; then \
		echo "error: version.go does not carry version $(VERSION); run make generate" >&2; exit 1; fi; \
	if ! grep -q '^version: $(VERSION)$$' CITATION.cff; then \
		echo "error: CITATION.cff does not carry version $(VERSION); run make generate" >&2; exit 1; fi; \
	for FNAME in installer.sh installer.ps1; do \
		if ! grep -q 'VERSION.*"$(VERSION)"' $$FNAME; then \
			echo "error: $$FNAME does not carry version $(VERSION); run make generate" >&2; exit 1; fi; \
	done; \
	for FNAME in docs/*.?.md; do \
		if ! head -4 $$FNAME | grep -qF "$(VERSION)"; then \
			echo "error: $$FNAME does not carry version $(VERSION); run make build" >&2; exit 1; fi; \
	done; \
	go vet ./... || exit 1; \
	go test -count=1 ./... || exit 1; \
	echo "check-release: codemeta.json, version.go, CITATION.cff, installers and docs/ carry $(VERSION); vet and tests pass"

release: generate build check-release setup_dist distribute_docs dist/Linux-x86_64 dist/Linux-aarch64 dist/macOS-x86_64 dist/macOS-arm64 dist/Windows-x86_64 dist/Windows-arm64
	@printf "\ncheck-release passed. Now run: ./release.bash   (or ./release.bash --dry-run first)\n\n"


.FORCE:
