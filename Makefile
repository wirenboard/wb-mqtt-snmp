.PHONY: all clean

PREFIX = /usr
DEB_TARGET_ARCH ?= armhf

ifeq ($(DEB_TARGET_ARCH),armel)
GO_ENV := GOARCH=arm GOARM=5 CC_FOR_TARGET=arm-linux-gnueabi-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),armhf)
GO_ENV := GOARCH=arm GOARM=6 CC_FOR_TARGET=arm-linux-gnueabihf-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),arm64)
GO_ENV := GOARCH=arm64 CC_FOR_TARGET=aarch64-linux-gnu-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),amd64)
GO_ENV := GOARCH=amd64 CC=x86_64-linux-gnu-gcc
endif
ifeq ($(DEB_TARGET_ARCH),i386)
GO_ENV := GOARCH=386 CC=i586-linux-gnu-gcc
endif

GO ?= go

all: wb-mqtt-snmp

clean:
	rm -f wb-mqtt-snmp

amd64:
	$(MAKE) DEB_TARGET_ARCH=amd64

wb-mqtt-snmp: main.go mqtt_snmp/*.go
	$(GO_ENV) $(GO) build -ldflags="-s -w"

install:
	mkdir -p $(DESTDIR)$(PREFIX)/share/wb-mqtt-snmp/
	mkdir -p $(DESTDIR)/etc/wb-configs.d/

	install -Dm0755 wb-mqtt-snmp -t $(DESTDIR)$(PREFIX)/bin
	install -Dm0644 wb-mqtt-snmp.conf.sample $(DESTDIR)/etc/wb-mqtt-snmp.conf.sample
	install -Dm0644 wb-mqtt-snmp.conf.sample $(DESTDIR)/etc/wb-mqtt-snmp.conf
	install -Dm0644 wb-mqtt-snmp.schema.json -t $(DESTDIR)$(PREFIX)/share/wb-mqtt-confed/schemas
	install -Dm0644 wb-mqtt-snmp.wbconfigs $(DESTDIR)/etc/wb-configs.d/19wb-mqtt-snmp

	cp -rv ./templates $(DESTDIR)$(PREFIX)/share/wb-mqtt-snmp/templates

test:
	cd mqtt_snmp && CC= $(GO) test -gcflags="-N -l" -cover

deb:
	$(GO_ENV) dpkg-buildpackage -b -a$(DEB_TARGET_ARCH) -us -uc
