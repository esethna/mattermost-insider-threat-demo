PLUGIN_ID := $(shell sed -n 's/^  "id": "\(.*\)",/\1/p' plugin.json)
VERSION := $(shell sed -n 's/^  "version": "\(.*\)",/\1/p' plugin.json)
BUNDLE := dist/$(PLUGIN_ID)-$(VERSION).tar.gz
LDFLAGS := -s -w

.PHONY: build bundle vet clean

build:
	cd server && for target in linux/amd64 linux/arm64 darwin/arm64; do \
	  GOOS=$${target%/*} GOARCH=$${target#*/} CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o dist/plugin-$${target%/*}-$${target#*/} . || exit 1; \
	done

vet:
	go vet ./...

bundle: vet build
	rm -rf dist && mkdir -p dist/$(PLUGIN_ID)/server dist/$(PLUGIN_ID)/webapp
	cp plugin.json dist/$(PLUGIN_ID)/
	cp -r assets dist/$(PLUGIN_ID)/
	cp -r server/dist dist/$(PLUGIN_ID)/server/
	cp -r webapp/dist dist/$(PLUGIN_ID)/webapp/
	cd dist && tar -czf $(notdir $(BUNDLE)) $(PLUGIN_ID)
	@shasum -a 256 $(BUNDLE)

clean:
	rm -rf dist server/dist
