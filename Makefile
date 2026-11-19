# Copyright (C) 2021 Toitware ApS. All rights reserved.
# Use of this source code is governed by an MIT-style license that can be
# found in the LICENSE file.

BUILD_DIR := $(CURDIR)/build
BUILD_SDK_DIR := $(CURDIR)/build/sdk
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

ifeq ($(OS),Windows_NT)
  EXE_SUFFIX=.exe
  DETECTED_OS=$(OS)
else
  EXE_SUFFIX=
  DETECTED_OS=$(shell uname)
endif

ifdef JAG_TOIT_REPO_PATH
SDK_PATH := $(JAG_TOIT_REPO_PATH)/build/host/sdk
S31_SETUP := $(wildcard $(JAG_TOIT_REPO_PATH)/tools/setup-idf-s31.sh)
else
SDK_PATH := $(BUILD_SDK_DIR)
endif

JAG_BINARY := jag$(EXE_SUFFIX)
JAG_ENTRY_POINT := $(CURDIR)/src/jaguar.toit
JAG_TOIT_SOURCES := $(shell find src -name '*.toit') package.lock package.yaml
JAG_GO_SOURCES := $(shell find cmd -name '*.go')

# Setup Go compilation flags.
GO_BUILD_FLAGS :=
GO_LINK_FLAGS += -X 'main.buildDate="$(BUILD_DATE)"'
ifdef JAG_BUILD_RELEASE
GO_LINK_FLAGS += -X 'main.buildMode=release'
endif

.PHONY: all
all: jag assets

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)

#############################
# Rules for the Jaguar binary
#############################
.PHONY: jag
jag: $(BUILD_DIR)/$(JAG_BINARY)

$(BUILD_DIR)/$(JAG_BINARY): $(JAG_GO_SOURCES) $(wildcard cmd/jag/commands/partitions/*.csv)
	$(GO_BUILD_FLAGS) go build -ldflags "$(GO_LINK_FLAGS)" -o $@ ./cmd/jag

#############################
# Rules for the Jaguar assets
#############################
.PHONY: assets
assets: $(BUILD_DIR)/assets/jaguar.snapshot

$(BUILD_DIR)/assets/jaguar.snapshot: install-dependencies
$(BUILD_DIR)/assets/jaguar.snapshot: $(SDK_PATH)/bin/toit$(EXE_SUFFIX)
$(BUILD_DIR)/assets/jaguar.snapshot: $(JAG_TOIT_SOURCES)
	mkdir -p $(BUILD_DIR)/assets
	$(SDK_PATH)/bin/toit$(EXE_SUFFIX) compile -O2 --snapshot -o $@ $(JAG_ENTRY_POINT)

.PHONY: install-dependencies
install-dependencies: $(SDK_PATH)/bin/toit$(EXE_SUFFIX)
	$(SDK_PATH)/bin/toit$(EXE_SUFFIX) pkg --project-root=$(CURDIR) install

############################################
# Rules to build with JAG_TOIT_REPO_PATH set
############################################

ifdef JAG_TOIT_REPO_PATH
all: $(JAG_TOIT_REPO_PATH)/build/esp32/firmware.envelope

JAG_TOIT_DEPENDENCIES  = $(SDK_PATH)/bin/toit$(EXE_SUFFIX)
JAG_TOIT_DEPENDENCIES += $(JAG_TOIT_REPO_PATH)/build/esp32/firmware.envelope

# We use a marker in the build directory to avoid
# recompiling the SDK multiple times during one
# invocation of this Makefile.
SDK_BUILD_MARKER := $(BUILD_DIR)/sdk.build
$(JAG_TOIT_DEPENDENCIES): force-rebuild-sdk
$(JAG_TOIT_DEPENDENCIES): $(SDK_BUILD_MARKER)

# The SDK build marker is *not* phony, so we only
# use the rule once per invocation of this Makefile.
$(SDK_BUILD_MARKER):
	make -C $(JAG_TOIT_REPO_PATH) version-file esp32
	make -C $(JAG_TOIT_REPO_PATH) install-esptool
	mkdir -p $(BUILD_DIR)
	echo "$(BUILD_DATE)" > $@

.PHONY: all-chips
all-chips:
	make -C $(JAG_TOIT_REPO_PATH) ESP32_CHIP=esp32c3 esp32
	make -C $(JAG_TOIT_REPO_PATH) ESP32_CHIP=esp32c6 esp32
	make -C $(JAG_TOIT_REPO_PATH) ESP32_CHIP=esp32s2 esp32
	make -C $(JAG_TOIT_REPO_PATH) ESP32_CHIP=esp32s3 esp32
ifneq ($(S31_SETUP),)
	$(S31_SETUP) --install
	make -C $(JAG_TOIT_REPO_PATH) ESP32_CHIP=esp32s31 esp32
endif

.PHONY: force-rebuild-sdk
force-rebuild-sdk:
	rm -f $(SDK_BUILD_MARKER)
endif

###############################################
# Rules to build without JAG_TOIT_REPO_PATH set
###############################################

.PHONY: download-sdk
download-sdk: $(BUILD_DIR)/$(JAG_BINARY)
	rm -rf $(BUILD_SDK_DIR)
	$(BUILD_DIR)/$(JAG_BINARY) --no-analytics setup sdk $(BUILD_SDK_DIR)

TEST_CHIPS := esp32 esp32c3 esp32c6 esp32s2 esp32s3
# The pinned release SDK has no S31 envelope. Test it with the source SDK.
ifneq ($(S31_SETUP),)
TEST_CHIPS += esp32s31
endif

.PHONY: test
test: $(BUILD_DIR)/$(JAG_BINARY)
	go test ./...
	@for test in tests/*-test.toit; do \
		echo "$(BUILD_DIR)/$(JAG_BINARY) toit run $$test"; \
		$(BUILD_DIR)/$(JAG_BINARY) toit run $$test || exit 1; \
	done
	@# Extract standard variants and S31 when the source SDK supports it.
	@failed=0; \
	for variant in $(TEST_CHIPS); do \
		tmp_dir=$$(mktemp -d) || exit 1; \
		if $(BUILD_DIR)/$(JAG_BINARY) \
				--no-analytics \
				--wifi-ssid=test --wifi-password=test \
				firmware extract $$variant \
				-o $$tmp_dir/firmware.bin; then \
			echo "PASS: $$variant firmware extraction"; \
		else \
			echo "FAIL: $$variant firmware extraction"; \
			failed=1; \
		fi; \
		rm -rf $$tmp_dir; \
	done; \
	exit $$failed
