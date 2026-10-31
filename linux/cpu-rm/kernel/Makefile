# SPDX-License-Identifier: GPL-2.0-only
# Set KDIR to build for a kernel other than the running one.
KDIR ?= /lib/modules/$(shell uname -r)/build
.PHONY: all clean
all clean:
	$(MAKE) -C "$(KDIR)" M="$(CURDIR)" $(if $(filter clean,$@),clean,modules)
