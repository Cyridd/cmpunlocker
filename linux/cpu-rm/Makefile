# SPDX-License-Identifier: GPL-2.0-only
.PHONY: all firmware offline-test gen2-test mkinitcpio-test clean
all:
	$(MAKE) -C kernel
firmware:
	python3 firmware/prepare.py
build/test_offline: test_offline.c kernel/boot.c kernel/prep.c kernel/layout.c kernel/vbios.c $(wildcard kernel/*.h)
	mkdir -p build
	$(CC) -std=c11 -O2 -Wall -Wextra -Werror test_offline.c kernel/boot.c kernel/prep.c kernel/layout.c kernel/vbios.c -o $@
gen2-test:
	python3 tests/run_gen2.py
	python3 tests/run_helpers.py
mkinitcpio-test:
	python3 tests/run_mkinitcpio.py
offline-test: build/test_offline gen2-test mkinitcpio-test
	cd build && ./test_offline $(if $(ROM),"$(abspath $(ROM))")
clean:
	$(MAKE) -C kernel clean
