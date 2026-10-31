# SPDX-License-Identifier: GPL-2.0-only
.PHONY: all firmware offline-test clean
all:
	$(MAKE) -C kernel
firmware:
	python3 firmware/prepare.py
build/test_offline: test_offline.c kernel/boot.c kernel/prep.c kernel/layout.c kernel/vbios.c $(wildcard kernel/*.h)
	mkdir -p build
	$(CC) -std=c11 -O2 -Wall -Wextra -Werror test_offline.c kernel/boot.c kernel/prep.c kernel/layout.c kernel/vbios.c -o $@
offline-test: build/test_offline
	cd build && ./test_offline $(if $(ROM),"$(abspath $(ROM))")
clean:
	$(MAKE) -C kernel clean
