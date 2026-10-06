.PHONY: all clean test install

PREFIX = /usr
DEB_TARGET_ARCH ?= armhf
WBGO_LOCAL_PATH ?= .

ifeq ($(DEB_TARGET_ARCH),armhf)
GO_ENV := GOARCH=arm GOARM=6 CC_FOR_TARGET=arm-linux-gnueabihf-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),arm64)
GO_ENV := GOARCH=arm64 CC_FOR_TARGET=aarch64-linux-gnu-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),amd64)
GO_ENV := GOARCH=amd64
endif

GO ?= go
GOTEST ?= $(GO) test
GCFLAGS :=
LDFLAGS :=
GO_FLAGS := -buildvcs=false
GO_TEST_FLAGS := -v -cover

ifeq ($(DEBUG),)
	LDFLAGS += -s -w
	GO_FLAGS += -trimpath
else
	GCFLAGS += -N -l
	GO_TEST_FLAGS += -failfast
endif

GO_FLAGS += $(if $(GCFLAGS),-gcflags=all="$(GCFLAGS)") $(if $(LDFLAGS),-ldflags="$(LDFLAGS)")

all: wb-mqtt-snmp

clean:
	rm -f wb-mqtt-snmp mqtt_snmp/wbgo.so

amd64:
	$(MAKE) DEB_TARGET_ARCH=amd64

wb-mqtt-snmp: main.go mqtt_snmp/*.go
	$(GO_ENV) $(GO) build $(GO_FLAGS)

test:
	cp $(WBGO_LOCAL_PATH)/amd64.wbgo.so mqtt_snmp/wbgo.so
	$(GOTEST) $(GO_FLAGS) $(GO_TEST_FLAGS) ./mqtt_snmp

install:
	install -Dm0755 wb-mqtt-snmp -t $(DESTDIR)$(PREFIX)/bin
	install -Dm0644 $(WBGO_LOCAL_PATH)/$(DEB_TARGET_ARCH).wbgo.so $(DESTDIR)$(PREFIX)/lib/wb-mqtt-snmp/wbgo.so
	install -Dm0644 wb-mqtt-snmp.conf.sample $(DESTDIR)/etc/wb-mqtt-snmp.conf.sample
	install -Dm0644 wb-mqtt-snmp.conf.sample $(DESTDIR)/etc/wb-mqtt-snmp.conf
	install -Dm0644 wb-mqtt-snmp.schema.json -t $(DESTDIR)$(PREFIX)/share/wb-mqtt-confed/schemas
	install -Dm0644 wb-mqtt-snmp.wbconfigs $(DESTDIR)/etc/wb-configs.d/19wb-mqtt-snmp
	install -Dm0644 templates/*.json -t $(DESTDIR)$(PREFIX)/share/wb-mqtt-snmp/templates
