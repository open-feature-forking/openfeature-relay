PHONY: .docker-build .build .run .mockgen
PREFIX=/usr/local
PUBLIC_JSON_SCHEMA_DIR=docs/schema/v0/
ALL_GO_MOD_DIRS := $(shell find . \( -path ./test/integration -o -path ./.claude -o -path ./test-harness -o -path ./spec -o -path ./schemas -o -path ./playground-app -o -path "./distributions/*/build" -o -path "./test/distributions/*/build" -o -path ./.flagd-generated -o -path ./.flagd-full-build \) -prune -o -type f -name 'go.mod' -exec dirname {} \; | sort)

# The library modules: what embedders import. Tests, the FIPS tests and the benchmarks run on exactly
# these (plus the binaries), and testdata/go-packages.txt pins the packages they contain.
GO_LIB_MODULE_DIRS := ./core ./sync/file ./sync/flagd-grpc ./sync/http ./sync/openfeature-operator ./sync/gcs ./sync/azblob ./sync/s3 ./sync/builder ./telemetry/otel ./evaluator/flagd ./configformat/flagd

# The modules flagd-full is made of besides the library ones: its main module, the runtime and the service endpoints
# (UC-001 Q7). They are tested, benchmarked and pinned like the library modules.
FLAGD_MODULE_DIRS := ./flagd ./runtime ./service/transport ./service/flag-evaluation ./service/ofrep ./service/flag-sync

# The FIPS variant builds against the CMVP-certified Go Cryptographic Module
# v1.0.0 (certificate #5247). The literal version is required; the
# "certified"/"inprocess" aliases vary by toolchain. The fips140 tag makes the
# binary require that module at startup rather than merely report on it.
FIPS_ENV  := GOFIPS140=v1.0.0 CGO_ENABLED=0
FIPS_TAGS := fips140

FLAGD_DEV_NAMESPACE ?= flagd-dev
ZD_TEST_NAMESPACE_FLAGD_PROXY ?= flagd-proxy-zd-test
ZD_TEST_NAMESPACE ?= flagd-zd-test
ZD_CLIENT_IMG ?= zd-client:latest
FLAGD_PROXY_IMG ?= flagd-proxy:latest
FLAGD_PROXY_IMG_ZD ?= flagd-proxy:zd

DOCS_DIR ?= docs

workspace-init: workspace-clean
	go work init
	$(foreach module, $(ALL_GO_MOD_DIRS), go work use $(module);)

workspace-update:
	$(foreach module, $(ALL_GO_MOD_DIRS), go work use $(module);)

workspace-clean:
	rm -rf go.work

guard-%:
	@ if [ "${${*}}" = "" ]; then \
        echo "Environment variable $* not set"; \
        exit 1; \
    fi
docker-build: # default to flagd
	make docker-build-flagd
docker-push: # default to flagd
	make docker-push-flagd
docker-build-flagd:
	docker buildx build --build-arg=VERSION="$$(git describe --tags --abbrev=0)" --build-arg=COMMIT="$$(git rev-parse --short HEAD)" --build-arg DATE="$$(date +%FT%TZ)" --platform="linux/arm64" -t ${IMG} -f flagd/build.Dockerfile .
docker-push-flagd:
	docker buildx build --push --build-arg=VERSION="$$(git describe --tags --abbrev=0)" --build-arg=COMMIT="$$(git rev-parse --short HEAD)" --build-arg DATE="$$(date +%FT%TZ)" --platform="linux/ppc64le,linux/s390x,linux/amd64,linux/arm64" -t ${IMG} -f flagd/build.Dockerfile .
build: workspace-init # default to flagd
	make build-flagd
build-flagd:
	go build -ldflags "-X main.version=dev -X main.commit=$$(git rev-parse --short HEAD) -X main.date=$$(date +%FT%TZ)" -o ./bin/flagd ./flagd

.PHONY: build-flagd-fips
build-flagd-fips:
	$(FIPS_ENV) go build -tags $(FIPS_TAGS) -ldflags "-X main.version=dev -X main.commit=$$(git rev-parse --short HEAD) -X main.date=$$(date +%FT%TZ)" -o ./bin/flagd-fips ./flagd
	go version -m ./bin/flagd-fips | grep -E 'GOFIPS140=|-tags='
.PHONY: test
# One target per line, so parallel pull requests that add a test target merge cleanly.
TEST_TARGETS += test-core
TEST_TARGETS += test-flagd
TEST_TARGETS += test-flagd-proxy
TEST_TARGETS += test-builder
TEST_TARGETS += test-distributions
TEST_TARGETS += test-upgrade-accepted
TEST_TARGETS += test-configformat-example
TEST_TARGETS += test-service-example
TEST_TARGETS += test-evaluator-testkit
TEST_TARGETS += test-inprocess-example
TEST_TARGETS += test-sync-example
TEST_TARGETS += test-telemetry-example
TEST_TARGETS += test-eventsink-example
TEST_TARGETS += test-contract-external
TEST_TARGETS += test-middleware-example
test: $(TEST_TARGETS)
test-core: test-modules
test-modules:
	@rc=0; for m in $(GO_LIB_MODULE_DIRS); do \
		go test -race -covermode=atomic -cover -short $$m/... -coverprofile=$$(echo $$m | sed 's#^\./##; s#/#-#g')-coverage.out || rc=1; \
	done; exit $$rc
test-flagd:
	go test -race -covermode=atomic -cover -short $(addsuffix /...,$(FLAGD_MODULE_DIRS)) -coverprofile=flagd-coverage.out
test-sync-example:
	go test -race -short -count=1 ./test/sync-example/...
test-telemetry-example:
	go test -race -short -count=1 ./test/telemetry-example/... ./test/telemetry-example/distribution/...
test-contract-external:
	./test/contract-external.sh
test-middleware-example:
	go test -race -short -count=1 ./test/middleware-example/... ./test/middleware-example/distribution/...
test-eventsink-example:
	go test -race -short -count=1 ./test/eventsink-example/... ./test/eventsink-example/distribution/...
test-configformat-example:
	go test -race -short -count=1 ./test/configformat-example/... ./test/configformat-example/distribution/...
test-service-example:
	go test -race -short -count=1 ./test/service-example/...
# The testbed's evaluator suite lives in the test-harness submodule (git submodule update --init test-harness).
test-evaluator-testkit:
	go test -race -short -count=1 ./test/evaluator-testkit/...
# The in-process examples assert on their own module graphs, which they read with the go command outside the workspace.
test-inprocess-example:
	go test -race -short -count=1 ./test/inprocess-example/file/... ./test/inprocess-example/filegrpc/... ./test/inprocess-example/engine/...
# The Builder is a tool of its own: it imports no relay module (UC-001 Q1).
test-builder:
	go test -race -short -count=1 ./builder/...
test-distributions:
	cd test/distributions && go test -count=1 -timeout 30m ./...
test-flagd-proxy:
	go test -race -covermode=atomic -cover -short ./flagd-proxy/pkg/... -coverprofile=flagd-proxy-coverage.out
# fips140=only makes non-approved algorithms error or panic, so this fails if a
# tested path reaches MD5, SHA-1, RC4, 3DES or ChaCha20-Poly1305. Test mode only;
# released binaries run with fips140=on.
#
# -exec applies the GODEBUG to the test binaries only. Setting it in the
# environment would also apply it to the go command, whose module fetches over
# TLS negotiate X25519 and fail under fips140=only.
.PHONY: test-fips
test-fips:
	$(FIPS_ENV) go test -tags $(FIPS_TAGS) -short -count=1 -exec 'env GODEBUG=fips140=only' $(addsuffix /...,$(GO_LIB_MODULE_DIRS)) $(addsuffix /...,$(FLAGD_MODULE_DIRS)) ./flagd-proxy/...

# Regenerate the crypto dependency closure recorded in the FIPS docs. Pinned to
# linux/amd64: the closure is platform-dependent, so an unpinned run would
# differ between a developer machine and CI.
.PHONY: fips-closure
fips-closure:
	@$(FIPS_ENV) GOOS=linux GOARCH=amd64 go list -tags $(FIPS_TAGS) -deps ./flagd ./flagd-proxy \
	  | grep -E '^(crypto|golang\.org/x/crypto)($$|/)|/crypto($$|/)' \
	  | sort -u > docs/reference/fips-crypto-closure.txt
	@echo "wrote docs/reference/fips-crypto-closure.txt"

flagd-benchmark-test:
	go test -bench=Bench -short -benchtime=5s -benchmem $(addsuffix /...,$(GO_LIB_MODULE_DIRS) $(FLAGD_MODULE_DIRS)) | tee benchmark.txt
flagd-integration-test-harness:
# target used to start a locally built flagd with the e2e flags
	cd flagd; go run main.go start -f file:../test-harness/flags/testing-flags.json -f file:../test-harness/flags/custom-ops.json -f file:../test-harness/flags/evaluator-refs.json -f file:../test-harness/flags/zero-flags.json -f file:../test-harness/flags/edge-case-flags.json
flagd-integration-test: workspace-clean
# this is a intentionally an "orphaned" module so that it effectively does e2e testing independently of the rest of the code
	cd test/integration && go test -count=1 -cover $(ARGS)
run: # default to flagd
	make run-flagd
run-flagd:
	cd flagd; go run main.go start -f file:../config/samples/example_flags.flagd.json 
run-flagd-selector-demo:
	cd flagd; go run main.go start -f file:../config/samples/example_flags.flagd.json -f file:../config/samples/example_flags.flagd.2.json
install:
	cp systemd/flagd.service /etc/systemd/system/flagd.service
	mkdir -p /etc/flagd
	cp systemd/flags.json /etc/flagd/flags.json
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	cp bin/flagd $(DESTDIR)$(PREFIX)/bin/flagd
	systemctl start flagd
uninstall:
	systemctl disable flagd
	systemctl stop flagd
	rm /etc/systemd/system/flagd.service
	rm -f $(DESTDIR)$(PREFIX)/bin/flagd
lint:
	$(MAKE) check-flagd-full-generated
	go install -v github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
	rc=0; $(foreach module, $(ALL_GO_MOD_DIRS), ${GOPATH}/bin/golangci-lint run $(module)/... || rc=1;) exit $$rc
lint-fix:
	go install -v github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
	rc=0; $(foreach module, $(ALL_GO_MOD_DIRS), ${GOPATH}/bin/golangci-lint run --fix $(module)/... || rc=1;) exit $$rc
# The packages of the library modules and of flagd-full's modules must not change unnoticed, so that a move cannot drop a package from the test run.
.PHONY: check-package-coverage
check-package-coverage:
	@for m in $(GO_LIB_MODULE_DIRS) $(FLAGD_MODULE_DIRS); do (cd $$m && go list ./...) || exit 1; done | LC_ALL=C sort > .go-packages.tmp; \
	tr -d '\r' < testdata/go-packages.txt > .go-packages.want; \
	if ! diff -u .go-packages.want .go-packages.tmp; then rm -f .go-packages.tmp .go-packages.want; \
		echo "the packages of the library modules differ from testdata/go-packages.txt"; exit 1; fi; \
	rm -f .go-packages.tmp .go-packages.want

# The modules flagd-full links must not change unnoticed (UC-001 pin 1c). A module that moves to another path is the
# only accepted change: update testdata/flagd-full-modules.txt in the same commit and say so in the commit message.
.PHONY: check-flagd-full-modules flagd-full-modules
flagd-full-modules:
	@go list -deps -f '{{with .Module}}{{.Path}}{{end}}' ./flagd | grep -v '^$$' | LC_ALL=C sort -u
check-flagd-full-modules:
	@$(MAKE) --no-print-directory flagd-full-modules > .flagd-full-modules.tmp; 	tr -d '' < testdata/flagd-full-modules.txt > .flagd-full-modules.want; 	if ! diff -u .flagd-full-modules.want .flagd-full-modules.tmp; then rm -f .flagd-full-modules.tmp .flagd-full-modules.want; 		echo "the modules flagd-full links differ from testdata/flagd-full-modules.txt"; exit 1; fi; 	rm -f .flagd-full-modules.tmp .flagd-full-modules.want

# testdata/module-boundaries.txt lists <module dir>:<packages>:<forbidden import path regexp>; no package of
# <packages>, nor anything it imports, may match.
.PHONY: check-module-boundary check-evaluator-boundary
# testdata/module-graph-boundaries.txt lists <module dir>:<forbidden module path regexp>; none of the modules in the
# module graph of <module dir> (built outside the workspace, as an embedder would) may match.
check-module-boundary:
	@while IFS=: read -r dir pkgs forbidden; do \
		forbidden=$$(printf '%s' "$$forbidden" | tr -d '\r'); \
		case "$$dir" in ''|'#'*) continue;; esac; \
		deps=$$(cd $$dir && go list -deps $$pkgs) || exit 1; \
		if echo "$$deps" | grep -E "$$forbidden"; then \
			echo "$$dir $$pkgs must not depend on the packages listed above"; exit 1; \
		fi; \
	done < testdata/module-boundaries.txt
	@while IFS=: read -r dir forbidden; do \
		forbidden=$$(printf '%s' "$$forbidden" | tr -d '\r'); \
		case "$$dir" in ''|'#'*) continue;; esac; \
		mods=$$(cd $$dir && GOWORK=off go list -m all) || exit 1; \
		if echo "$$mods" | grep -E "$$forbidden"; then \
			echo "$$dir must not have the modules listed above in its module graph"; exit 1; \
		fi; \
	done < testdata/module-graph-boundaries.txt
check-evaluator-boundary: check-module-boundary
install-mockgen:
	go install go.uber.org/mock/mockgen@v0.4.0
mockgen: install-mockgen
	cd sync/http; mockgen -source=http_sync.go -destination=mock/http.go -package=syncmock
	cd sync/flagd-grpc; mockgen -source=grpc_sync.go -destination=mock/grpc.go -package=grpcmock
	cd sync/flagd-grpc; mockgen -source=credentials/builder.go -destination=credentials/mock/builder.go -package=credendialsmock
	cd core; mockgen -source=pkg/evaluator/ievaluator.go -destination=pkg/evaluator/mock/ievaluator.go -package=evalmock
	cd sync/builder; mockgen -source=syncbuilder.go -destination=mock/syncbuilder.go -package=middlewaremocksyncbuildermock
	cd service/transport; mockgen -source=middleware/interface.go -destination=middleware/mock/interface.go -package=middlewaremock
	cd service/flag-evaluation; mockgen -source=flag_evaluator_v2.go -destination=mock/eventstream.go -package=flagevalmock
generate-docs:
	cd flagd; go run ./cmd/doc/main.go

.PHONY: deploy-dev-env
export IMG?= ghcr.io/open-feature/flagd:latest
deploy-dev-env: undeploy-dev-env
	kubectl create ns "$(FLAGD_DEV_NAMESPACE)"
	envsubst '$${IMG}' < config/deployments/flagd/deployment.yaml | kubectl apply -f - -n "$(FLAGD_DEV_NAMESPACE)"
	kubectl apply -f config/deployments/flagd/service.yaml -n "$(FLAGD_DEV_NAMESPACE)"
	kubectl wait --for=condition=available deployment/flagd -n "$(FLAGD_DEV_NAMESPACE)" --timeout=300s

undeploy-dev-env:
	kubectl delete ns "$(FLAGD_DEV_NAMESPACE)" --ignore-not-found=true

run-zd-test:
	kubectl delete ns "$(ZD_TEST_NAMESPACE)" --ignore-not-found=true
	kubectl create ns "$(ZD_TEST_NAMESPACE)"
	ZD_TEST_NAMESPACE="$(ZD_TEST_NAMESPACE)" FLAGD_DEV_NAMESPACE=$(FLAGD_DEV_NAMESPACE) IMG="$(IMG)" IMG_ZD="$(IMG_ZD)" ./test/zero-downtime/zd_test.sh

run-flagd-proxy-zd-test:
	ZD_TEST_NAMESPACE_FLAGD_PROXY="$(ZD_TEST_NAMESPACE_FLAGD_PROXY)" FLAGD_PROXY_IMG="$(FLAGD_PROXY_IMG)" FLAGD_PROXY_IMG_ZD="$(FLAGD_PROXY_IMG_ZD)" ZD_CLIENT_IMG="$(ZD_CLIENT_IMG)" ./test/zero-downtime-flagd-proxy/zd_test.sh

# Markdown lint configuration
#
# - .markdownlintignore holds the configuration for files to be ignored
# - .markdownlint.yaml contains the rules for markdownfiles
MDL_DOCKER_VERSION := next
ROOT_DIR := $(dir $(realpath $(lastword $(MAKEFILE_LIST))))
MDL_CMD := docker run -v $(ROOT_DIR):/workdir --rm 

.PHONY: markdownlint markdownlint-fix
markdownlint:
	$(MDL_CMD) davidanson/markdownlint-cli2:$(MDL_DOCKER_VERSION) "**/*.md" 

markdownlint-fix:
	$(MDL_CMD) davidanson/markdownlint-cli2:$(MDL_DOCKER_VERSION) --fix "**/*.md" 

.PHONY: pull-schemas-submodule
pull-schemas-submodule:
	git submodule update schemas

.PHONY: generate-proto-docs
generate-proto-docs: pull-schemas-submodule
	docker run --rm -v ${PWD}/$(DOCS_DIR)/reference/specifications:/out -v ${PWD}/schemas/protobuf:/protos pseudomuto/protoc-gen-doc --doc_opt=markdown,protos-with-toc.md flagd/evaluation/v1/evaluation.proto flagd/sync/v1/sync.proto \
	&& echo '<!-- WARNING: THIS DOC IS AUTO-GENERATED. DO NOT EDIT! -->' > ${PWD}/$(DOCS_DIR)/reference/specifications/protos.md \
	&& sed '/^## Table of Contents/,/#top/d' ${PWD}/$(DOCS_DIR)/reference/specifications/protos-with-toc.md >> ${PWD}/$(DOCS_DIR)/reference/specifications/protos.md \
	&& rm -f ${PWD}/$(DOCS_DIR)/reference/specifications/protos-with-toc.md

# Update the schema at flagd.dev
# PUBLIC_JSON_SCHEMA_DIR above controls the dir (and therefore major version)
.PHONY: update-public-schema
update-public-schema: pull-schemas-submodule
	rm -f $(PUBLIC_JSON_SCHEMA_DIR)*.json
	cp schemas/json/*.json $(PUBLIC_JSON_SCHEMA_DIR)

.PHONY: run-web-docs
run-web-docs: generate-docs generate-proto-docs
	docker build -t flag-docs:latest .  --load \
	&& docker run --rm -it -p 8000:8000 -v ${PWD}:/docs flag-docs:latest

# Run the playground app in dev mode
# See the readme in the playground-app folder for more details
.PHONY: playground-dev
playground-dev:
	cd playground-app && npm ci && npm run dev

# Build the playground app
# See the readme in the playground-app folder for more details
.PHONY: playground-build
playground-build:
	cd playground-app && npm ci && npm run build

# Publish the playground app to the docs folder
# See the readme in the playground-app folder for more details
.PHONY: playground-publish
playground-publish: playground-build
	cp playground-app/dist/assets/index-*.js docs/playground/playground.js

# UC-009: the flagd module is the Builder's output for distributions/flagd-full/manifest.yaml. The Builder generates its
# go.mod, main.go and components/components.go; this target regenerates them into a directory beside flagd/, so that
# the replaces come out the same, and fails when what is committed differs. A hand edit of a generated file, or a
# manifest edit without the regenerated files, fails here.
FLAGD_FULL_CHECK_DIR := .flagd-generated
.PHONY: check-flagd-full-generated
check-flagd-full-generated:
	cd builder && go build -o ../bin/relay-builder ./cmd/relay-builder
	rm -rf $(FLAGD_FULL_CHECK_DIR)
	cp -r flagd $(FLAGD_FULL_CHECK_DIR)
	GOWORK=off ./bin/relay-builder --manifest distributions/flagd-full/manifest.yaml --skip-compile --output $(FLAGD_FULL_CHECK_DIR) \
		|| { rm -rf $(FLAGD_FULL_CHECK_DIR); exit 1; }
	rc=0; for f in go.mod main.go components/components.go; do \
		diff -u flagd/$$f $(FLAGD_FULL_CHECK_DIR)/$$f || rc=1; done; \
		rm -rf $(FLAGD_FULL_CHECK_DIR); \
		if [ $$rc -ne 0 ]; then echo "flagd/ differs from the Builder's output for distributions/flagd-full/manifest.yaml; run: make generate-flagd-full"; fi; \
		exit $$rc

# Regenerates the flagd module from the manifest of flagd-full.
.PHONY: generate-flagd-full
generate-flagd-full:
	cd builder && go build -o ../bin/relay-builder ./cmd/relay-builder
	GOWORK=off ./bin/relay-builder --manifest distributions/flagd-full/manifest.yaml --skip-compile

# UC-001 7a: the Builder builds flagd-full from its manifest, the binary is named flagd, and the duration is printed
# (NFR-008 asks for under three minutes). Run it with a warm module cache. The binary and the pins that run against it
# stay out of flagd/.
FLAGD_FULL_BUILD_DIR := .flagd-full-build
.PHONY: build-flagd-full
build-flagd-full:
	cd builder && go build -o ../bin/relay-builder ./cmd/relay-builder
	rm -rf $(FLAGD_FULL_BUILD_DIR)
	start=$$(date +%s); \
	GOWORK=off ./bin/relay-builder --manifest distributions/flagd-full/manifest.yaml --output $(FLAGD_FULL_BUILD_DIR) || exit 1; \
	echo "flagd-full built in $$(( $$(date +%s) - start ))s"
	test -x $(FLAGD_FULL_BUILD_DIR)/flagd
	cd flagd && FLAGD_BINARY=$$(cd ../$(FLAGD_FULL_BUILD_DIR) && pwd)/flagd go test -count=1 -run "TestCLICharacterization|TestStartupLogCharacterization" ./

# UC-001, UC-009: the flagd module is what the Builder generates, and the Builder's binary passes the pins.
.PHONY: builder-e2e
builder-e2e: check-flagd-full-generated build-flagd-full

# UC-009: the differential of today's flagd against the flagd-full the Builder made. It builds two images, so it needs
# Docker and stays out of `make test`; TestAcceptedMatchesIssue does not need Docker and runs there.
.PHONY: upgrade-test test-upgrade-accepted refresh-issue-78
upgrade-test:
	cd test/upgrade && UPGRADE_TEST=1 go test -count=1 -timeout 60m -v ./...

test-upgrade-accepted:
	cd test/upgrade && go test -count=1 -run 'TestAcceptedMatchesIssue|TestSurfaces|TestGuideCoversAcceptedItems' ./...

# Refreshes the list of behaviour changes that accepted.yaml has to cover.
refresh-issue-78:
	gh issue view 78 --repo open-feature-forking/openfeature-relay --json body --jq .body > test/upgrade/testdata/issue-78.txt
