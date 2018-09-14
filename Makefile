.PHONY: all prepare clean

DEB_TARGET_ARCH ?= armel

ifeq ($(DEB_TARGET_ARCH),armel)
GO_ENV := GOARCH=arm GOARM=5 CC_FOR_TARGET=arm-linux-gnueabi-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),armhf)
GO_ENV := GOARCH=arm GOARM=6 CC_FOR_TARGET=arm-linux-gnueabihf-gcc CC=$$CC_FOR_TARGET CGO_ENABLED=1
endif
ifeq ($(DEB_TARGET_ARCH),amd64)
GO_ENV := GOARCH=amd64 CC=x86_64-linux-gnu-gcc
endif
ifeq ($(DEB_TARGET_ARCH),i386)
GO_ENV := GOARCH=386 CC=i586-linux-gnu-gcc
endif

all: wb-mqtt-snmp

clean:
	rm -f wb-mqtt-snmp

amd64:
	$(MAKE) DEB_TARGET_ARCH=amd64

wb-mqtt-snmp: main.go mqtt_snmp/*.go
	$(GO_ENV) glide install
	$(GO_ENV) go build

install:
	mkdir -p $(DESTDIR)/usr/share/wb-mqtt-confed/schemas/
	mkdir -p $(DESTDIR)/usr/share/wb-mqtt-snmp/
	mkdir -p $(DESTDIR)/etc/wb-configs.d/
	mkdir -p $(DESTDIR)/usr/bin/ $(DESTDIR)/etc/init.d/

	install -m 0755 wb-mqtt-snmp $(DESTDIR)/usr/bin/
	install -m 0644 wb-mqtt-snmp.conf.sample $(DESTDIR)/etc/wb-mqtt-snmp.conf.sample
	install -m 0644 wb-mqtt-snmp.conf.sample $(DESTDIR)/etc/wb-mqtt-snmp.conf
	install -m 0644 wb-mqtt-snmp.schema.json $(DESTDIR)/usr/share/wb-mqtt-confed/schemas/wb-mqtt-snmp.schema.json

	cp -rv ./templates $(DESTDIR)/usr/share/wb-mqtt-snmp/templates

test:
	cd mqtt_snmp && CC= go test -cover

deb: prepare
	CC=arm-linux-gnueabi-gcc dpkg-buildpackage -b -aarmel -us -uc
