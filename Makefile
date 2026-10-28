GO := $(if $(wildcard .tools/go/bin/go),$(CURDIR)/.tools/go/bin/go,go)

.PHONY: build vision setup test app demo demo-four
setup:
	./scripts/setup.sh
build: vision
	@mkdir -p build
	$(GO) build -trimpath -o build/agent-phone ./cmd/agent-phone
	xcrun --sdk iphonesimulator clang -fobjc-arc -O2 -arch arm64 -mios-simulator-version-min=17.0 -framework Foundation native/main.m native/SimulatorActions.m native/SimulatorTargets.m -o build/AgentPhoneActions
vision:
	@mkdir -p build
	xcrun --sdk macosx clang -fobjc-arc -O2 -arch arm64 -framework Foundation -framework CoreGraphics -framework CoreImage -framework ImageIO -framework IOSurface native/AgentPhoneVision.m -o build/AgentPhoneVision
test:
	$(GO) test -race ./...
	python3 -m unittest discover -s scripts -p 'test_*.py'
app: build
	./scripts/build-app.sh
demo: build
	./scripts/setup-wikipedia.sh
	python3 scripts/smoke-wikipedia.py --prepare
demo-four: build
	./scripts/setup-wikipedia.sh
	AGENT_PHONE_BUDGET_MIB=6144 python3 scripts/smoke-wikipedia.py --prepare --matrix examples/wikipedia-four.json --minimum-budget-mib 6144 --queries Saturn Volcano Jupiter Forest --output .cache/wikipedia-four-smoke.json
