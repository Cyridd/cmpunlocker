# cmpunlocker

[中文](README.md) | English

Github: https://github.com/pearlfortune/cmpunlocker

Unlocks the compute limiter on NVIDIA CMP 170HX / 90HX / 50HX cards you own.
Linux x86-64, root required.

It does not flash the VBIOS and cannot brick the card. If your driver / kernel /
VBIOS is not in the table below, the tool refuses to run rather than writing anything.



## Tested Environments

| Card | PCI ID | Driver | Kernel | VBIOS |
| --- | --- | --- | --- | --- |
| CMP 170HX | `10de:20c2` | `590.48.01` / `595.71.05` | any | any |
| CMP 170HX | `10de:20c2` | `610.43.03` | any | any |
| CMP 90HX | `10de:220d` | Open `580.159.03` | `6.10.0-hiveos` | `94.02.74.00.01` |
| CMP 90HX | `10de:220d` | Open `610.43.03` | `6.10.0-hiveos` | `94.02.74.00.01` / `94.02.74.00.05` |
| CMP 50HX | `10de:1e09` | Open `580.159.03` | `6.8.0-136-generic` | any |
| CMP 50HX | `10de:1e09` | Open `580.173.02` | `6.1.0-hiveos` | any |
| CMP 50HX | `10de:1e09` | Open `610.43.03` (persistent stockflow) | `6.1.0-hiveos` / `6.10.0-hiveos` | any |

Check what you have:

```sh
# List NVIDIA cards; note the PCI ID and the BDF (like 0000:01:00.0)
lspci -Dnn | grep -i nvidia

# Current NVIDIA driver version
modinfo -F version nvidia

# Current kernel version
uname -r
```

---

# 1. Temporary unlock

Lost on reboot — just run it again. To revert, simply reboot.



## 1.1 Download the binary

Download this once; every card below uses the same binary. **Run one command at a
time** so you can see exactly which step fails.

```sh
# Work in a temp directory
cd /var/tmp

# Download the binary bundle
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.27/cmpunlocker-v0.1.27-linux-x64-cli.tar.gz

# Download the checksum file
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.27/SHA256SUMS

# Verify the download; you must see OK. Anything else means an incomplete file - delete and retry
sha256sum -c SHA256SUMS --ignore-missing

# Extract
tar vxzf cmpunlocker-v0.1.27-linux-x64-cli.tar.gz

# Enter the extracted directory; every later command runs from here
cd cmpunlocker-v0.1.27-linux-x64-cli

# Confirm it runs - this prints the version
./cmpunlocker-rs --version
```



## 1.2 Run the unlock command

Run these from the directory extracted above. Pick the block matching your card.



#### CMP 170HX (driver 590.48.01 / 595.71.05)

```sh
# Unlock every 170HX. --quiesce first stops miner / watchdog / Xorg holding the GPU
sudo ./cmpunlocker-rs compute590 run \
--all-cmp170hx \
--acknowledge I-ACCEPT-590-FLEET-COMPUTE-TRANSACTION \
--quiesce
```

Success: `RESULT=success_compute_active`



#### CMP 170HX (driver 610.43.03)

You must add `--profile 610.43.03-compute`. Without it the run is a read-only check and changes nothing.

```sh
# The 610 driver requires the compute profile to be named explicitly
sudo ./cmpunlocker-rs compute590 run \
--profile 610.43.03-compute \
--all-cmp170hx \
--acknowledge I-ACCEPT-590-FLEET-COMPUTE-TRANSACTION \
--quiesce
```

Success: `RESULT=success_compute_active`



#### CMP 90HX

```sh
# Unlock every 90HX
sudo ./cmpunlocker-rs compute90hx-v67 run \
--all-cmp90hx \
--acknowledge I-ACCEPT-90HX-V67-COMPUTE-UNLOCK

# Re-check the state. This is read-only and can be run on its own at any time
sudo ./cmpunlocker-rs compute90hx-v67 verify --all-cmp90hx --expect full
```

Success: `PASS_CMP90HX_ALL_TARGETS_FULL_SPEED`



#### CMP 50HX

This uses the same `cmpunlocker-rs` binary as above — nothing extra to download. The tool auto-selects the embedded tuple by live driver / kernel (`580.159.03` + `6.8.0-136-generic` or `580.173.02` + `6.1.0-hiveos`).

```sh
# Read-only preflight first
sudo ./cmpunlocker-rs compute50hx-v534 preflight --all-cmp50hx

# Unlock every 50HX. --probe-unsupported-subsystems also probes OEM cards but only activates those that pass the full-speed gate
sudo ./cmpunlocker-rs compute50hx-v534 run \
--all-cmp50hx \
--probe-unsupported-subsystems \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK

# Re-check the state. This is read-only and can be run on its own at any time
sudo ./cmpunlocker-rs compute50hx-v534 verify --all-cmp50hx --expect full
```

Success: `PASS_CMP50HX_ALL_TARGETS_V534_HANDOFF_FULL_SPEED`, then `PASS_CMP50HX_ALL_TARGETS_FULL_SPEED` after `verify`.

OEM / ID=4 cards (subsystem `1462:371f`) cannot be activated by this V534 path — use **2.3 CMP 50HX persistent compute unlock** below. Running it stops the miner / watchdog, so do not run it during a production window.

---



# 2. Persistent unlock

Survives reboots. Kernel modules must be **built on the target host**, using a
separate bundle. You must rebuild and reinstall after any kernel or driver change.

Requirements: `/lib/modules/$(uname -r)/build`, `make`, `gcc`, `patch`, `binutils`, and Secure Boot off.

## 2.1 CMP 90HX persistent compute unlock

CMP 90HX persistence is currently a **610.43.03 engineering preview**. It has
passed three reboot cycles on one 8021/hive2222 card with VBIOS
`94.02.74.00.01`, and 8024/xinxitong has validated installing a prebuilt
artifact directly to full-speed. It does not create a systemd service. Kernel
changes, driver changes, VBIOS `94.02.74.00.05`, and multi-card installation
still need separate validation; v0.1.26 rejoin14 fixes the multi-GPU state
isolation issue found in rejoin13.

Environment: **NVIDIA Open `610.43.03` + kernel `6.10.0-hiveos` + CMP 90HX
`10de:220d` / `10de:1555` + VBIOS `94.02.74.00.01` only**.

#### **Step 1 — confirm the current environment**

```sh
# The kernel must be 6.10.0-hiveos
uname -r

# The NVIDIA driver must be 610.43.03
modinfo -F version nvidia

# This must be the open kernel module; Dual MIT/GPL is expected
modinfo -F license nvidia

# Confirm that CMP 90HX is visible
nvidia-smi -L
```

#### **Step 2 — download and verify the 90HX stockflow bundle**

```sh
VERSION=v0.1.27
ASSET="cmpunlocker-${VERSION}-linux-x64-90hx-stockflow"
BASE="https://github.com/pearlfortune/cmpunlocker/releases/download/${VERSION}"

cd /var/tmp

# Download the 90HX persistence bundle
wget -c "${BASE}/${ASSET}.tar.gz"

# Download the checksum file and verify the bundle; you must see OK
wget -c "${BASE}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing

# Extract and enter the 90HX stockflow directory
tar vxzf "${ASSET}.tar.gz"
cd "${ASSET}/stockflow/610.43.03"
```

#### **Step 3 — prepare NVIDIA's official source and build the artifact**

```sh
# Prepare NVIDIA's official open kernel source, or replace SOURCE with your local file
wget -c https://download.nvidia.com/XFree86/NVIDIA-kernel-module-source/NVIDIA-kernel-module-source-610.43.03.tar.xz
SOURCE="${PWD}/NVIDIA-kernel-module-source-610.43.03.tar.xz"

# Build the rejoin14-multigpu-state artifact
CMP90_STOCKFLOW_VARIANT=rejoin14 ./build-candidate.sh --source-tarball "${SOURCE}"
ART="artifacts/610.43.03-$(uname -r)-rejoin14-multigpu-state"

# Confirm the artifact matches the current driver and kernel
modinfo -F version "${ART}/nvidia.ko"
modinfo -F vermagic "${ART}/nvidia.ko"
strings "${ART}/nvidia.ko" | grep -E 'CMP90_STOCKFLOW_REJOIN12|CMP90_STOCKFLOW_REJOIN14'
```

#### **Step 4 — install and reboot**

```sh
# Install into an isolated updates directory. This does not hot-unload the driver.
sudo ./stockflow-install.sh \
--artifact "${ART}" \
--acknowledge I-ACCEPT-90HX-STOCKFLOW-PERSISTENT-INSTALL
sudo reboot
```

#### **Step 5 — verify after reboot**

```sh
cd /var/tmp/cmpunlocker-v0.1.27-linux-x64-90hx-stockflow/stockflow/610.43.03

# Re-check after reboot
BIN=../../cmpunlocker-rs
sudo "$BIN" compute90hx-v67 verify --all-cmp90hx --expect full

# Confirm that module resolution points to the stockflow directory
modinfo -n nvidia
```

Success: `PASS_CMP90HX_FULL_SPEED` or `PASS_CMP90HX_ALL_TARGETS_FULL_SPEED`.

Since v0.1.25, the installer writes
`/etc/depmod.d/cmpunlocker-90hx-stockflow.conf` so reboot-time module resolution
prefers `updates/cmpunlocker-90hx-stockflow` instead of the DKMS stock modules.
If a repeated install returns `PASS_CMP90HX_STOCKFLOW_ALREADY_INSTALLED`, the
host is already on the persistent stockflow resolution path.

#### **Restore the stock module resolution path**

```sh
cd /var/tmp/cmpunlocker-v0.1.27-linux-x64-90hx-stockflow/stockflow/610.43.03

# The restore script only removes the persistent module resolution path.
# It does not hot-unload the running driver; reboot after it completes.
sudo ./stockflow-restore.sh --acknowledge I-ACCEPT-90HX-STOCKFLOW-RESTORE
sudo reboot

# Confirm that the host is back on the stock module path
modinfo -n nvidia

# Optional: confirm that the card is back to locked state
cd /var/tmp/cmpunlocker-v0.1.27-linux-x64-90hx-stockflow/stockflow/610.43.03
BIN=../../cmpunlocker-rs
sudo "$BIN" compute90hx-v67 verify --all-cmp90hx --expect locked
```



## 2.2 CMP 170HX persistent VRAM unlock

Unlocks the capped visible VRAM and survives reboots. On the machines tested here
each card went from `8192 MiB` to `65536 MiB`; what you actually get depends on your card.

Requires **stock `610.43.03` open kernel module only**, built on the target host — a `.ko` from another machine will not work.



#### **Step 1 — switch to the stock 610.43.03 driver** (HiveOS example):

```sh
cd /var/tmp

# Download NVIDIA's official driver runfile
wget -c https://download.nvidia.com/XFree86/Linux-x86_64/610.43.03/NVIDIA-Linux-x86_64-610.43.03.run

# Upgrade using the HiveOS helper; on other systems use your own driver install method
/hive/sbin/nvidia-driver-update /var/tmp/NVIDIA-Linux-x86_64-610.43.03.run

# Confirm the version is now 610.43.03
modinfo -F version nvidia
```



#### **Step 2 — download and extract into a normal user's home.**

**The build must run as a normal user; running it as root is refused:**

```sh
# Create a dedicated build user (skipped if it already exists)
id cmpbuild >/dev/null 2>&1 || useradd -m -s /bin/bash cmpbuild

cd /home/cmpbuild

# Download the VRAM unlock bundle
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.27/cmpunlocker-v0.1.27-linux-x64-170hx-64g.tar.gz

# Extract
tar vxzf cmpunlocker-v0.1.27-linux-x64-170hx-64g.tar.gz

# Hand the directory to the build user, otherwise the next step cannot write to it
chown -R cmpbuild:cmpbuild /home/cmpbuild/cmpunlocker-v0.1.27-linux-x64-170hx-64g
```

The bundle already ships NVIDIA's official 610.43.03 open kernel source, so nothing else needs downloading.



#### **Step 3 — build (normal user), install (root), reboot**:

```sh
# Build the kernel modules as the normal user; takes a few minutes
su -s /bin/bash cmpbuild -c '
cd /home/cmpbuild/cmpunlocker-v0.1.27-linux-x64-170hx-64g
./build.sh --all-supported-cmp170hx \
--acknowledge I-ACCEPT-UNVERIFIED-610-MEMORY-KERNEL-BUILD'

cd /home/cmpbuild/cmpunlocker-v0.1.27-linux-x64-170hx-64g

# Install as root; this writes /lib/modules and updates the initramfs
sudo ./install.sh --all-supported-cmp170hx \
--acknowledge I-ACCEPT-UNVERIFIED-610-MEMORY-KERNEL-INSTALL

# A reboot is required for it to take effect
sudo reboot
```



#### **Step 4 — verify after reboot**:

```sh
# Each card should report more VRAM than before
nvidia-smi

# Confirm the loaded module is the unlocked one, not the stock module
modinfo -n nvidia
```

Success: `nvidia-smi` reports more VRAM than before the unlock, and `modinfo` prints a
path containing `updates/cmpunlocker-610-memory/nvidia.ko`.



#### **To revert**

Two stages. The first only removes the module directory and rebuilds the initramfs; it does not hot-unload the running driver:

```sh
cd /home/cmpbuild/cmpunlocker-v0.1.27-linux-x64-170hx-64g

# Stage one: remove the modules
sudo ./remove.sh --acknowledge REMOVE-CMPUNLOCKER-610-MEMORY-WITHOUT-HOT-UNLOAD
```



Then shut down, pull AC power, cold boot, and run the second stage to confirm:

```sh
cd /home/cmpbuild/cmpunlocker-v0.1.27-linux-x64-170hx-64g

# Stage two: confirm the card is back to stock after the cold boot
sudo ./remove.sh --confirm-cold-cycle \
--acknowledge I-CONFIRM-FULL-AC-POWER-CYCLE-AFTER-610-MEMORY-REMOVAL
```

---



## 2.3 CMP 50HX persistent compute unlock

Turns the 50HX compute unlock into a patched open driver that survives reboots. It
is also the only path that covers OEM / ID=4 cards (subsystem `1462:371f`). It must
be **built on the target host**, and rebuilt after any kernel or driver change.

Environment: **NVIDIA Open `580.173.02` + kernel `6.1.0-hiveos`, or NVIDIA Open
`610.43.03` + kernel `6.1.0-hiveos` / `6.10.0-hiveos`, with CMP 50HX `10de:1e09`**
(subsystem `10de:1554` or `1462:371f`) only.

v0.1.27 fixes a false failure on some warm/FLR transitions where the signed Booter
returns `mailbox1=4`. That value is accepted only when the WPR, FECS, RESET, and
speed states all match exactly. `610.43.03 + 6.10.0-hiveos` passed single-card and
six-card probes, persistent installation, and three consecutive reboot validations
on six `1462:371f` cards. Every cycle was 6/6 full-speed with no Booter, GSP,
`RmInitAdapter`, or Xid hard errors.

Requirements, as above: `/lib/modules/$(uname -r)/build`, `make`, `gcc`, `patch`, `binutils`, and Secure Boot off.

#### **Step 1 — download and verify the 50HX stockflow bundle**

```sh
VERSION=v0.1.27
ASSET="cmpunlocker-${VERSION}-linux-x64-50hx-stockflow"
BASE="https://github.com/pearlfortune/cmpunlocker/releases/download/${VERSION}"

cd "$HOME"

# Download the 50HX persistence bundle
wget -c "${BASE}/${ASSET}.tar.gz"

# Download the checksum file and verify the bundle; you must see OK
wget -c "${BASE}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing

# Extract and enter the bundle root; it ships ./cmpunlocker-rs at the top level
tar vxzf "${ASSET}.tar.gz"
cd "${ASSET}"
BIN=./cmpunlocker-rs
```

#### **Step 2 — fetch NVIDIA's source for the live driver and build the artifact**

```sh
# Select the source and build directory by the current NVIDIA driver
DRIVER="$(modinfo -F version nvidia)"
case "${DRIVER}" in
  580.173.02) SOURCE="NVIDIA-kernel-module-source-580.173.02.tar.xz"; WORK="stockflow/580.173.02" ;;
  610.43.03)  SOURCE="NVIDIA-kernel-module-source-610.43.03.tar.xz";  WORK="stockflow/610.43.03"  ;;
  *) echo "unsupported 50HX stockflow driver: ${DRIVER}" >&2; exit 2 ;;
esac

# Download NVIDIA's official open kernel source (public; the target host can fetch it directly)
wget -c "https://download.nvidia.com/XFree86/NVIDIA-kernel-module-source/${SOURCE}"

# Build the stock-flow artifact on the target host; takes a few minutes
cd "${WORK}"
./build-candidate.sh --source-tarball "../../${SOURCE}"
cd ../..

# Path to the built artifact
ART="${WORK}/artifacts/${DRIVER}-$(uname -r)-v551-stockflow"
```

#### **Step 3 — probe non-persistently, then install persistently and reboot**

```sh
# Non-persistent probe first: confirm the artifact reaches full-speed; it restores the stock driver on success
sudo "$BIN" compute50hx-v534 stockflow-probe \
--all-cmp50hx \
--stockflow-candidate "${ART}" \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK

# Persistent install: backs up the current modules and installs the 5 .ko from the artifact. It does not reboot automatically
sudo "$BIN" compute50hx-v534 stockflow-install \
--stockflow-candidate "${ART}" \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK

# Note the BACKUP_DIR= printed by the command above (needed to revert), then reboot
sudo reboot
```

#### **Step 4 — verify after reboot**

```sh
cd "$HOME/cmpunlocker-v0.1.27-linux-x64-50hx-stockflow"
BIN=./cmpunlocker-rs

# Confirm all 50HX are visible
nvidia-smi -L

# Read-only re-check; every card should be full-speed
sudo "$BIN" compute50hx-v534 verify --all-cmp50hx --expect full
```

Success: `PASS_CMP50HX_ALL_TARGETS_FULL_SPEED`. Rerun Step 2's `build-candidate.sh` after any kernel or driver change.

#### **Restore the pre-install modules**

Use the `BACKUP_DIR` printed by `stockflow-install` in Step 3 (looks like
`/var/lib/cmpunlocker-rs/transactions/compute50hx-v534-stockflow-install-<timestamp>/installed-module-backup`):

```sh
cd "$HOME/cmpunlocker-v0.1.27-linux-x64-50hx-stockflow"
BIN=./cmpunlocker-rs

# Restore the pre-install stock modules; reboot after it completes
sudo "$BIN" compute50hx-v534 stockflow-restore \
--backup-dir /var/lib/cmpunlocker-rs/transactions/compute50hx-v534-stockflow-install-<timestamp>/installed-module-backup \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK
sudo reboot
```

---



## Notes

- Single card: replace `--all-cmp170hx` / `--all-cmp90hx` / `--all-cmp50hx` with `--target-bdf 0000:01:00.0`, using the BDF from the `lspci` output above.
- Running it unloads and reloads the NVIDIA driver and stops your miner — do not run it during a production window.
- On failure, read the JSON transaction report under `/var/lib/cmpunlocker-rs/transactions/`; it contains the full reason.
- Full flags: `./cmpunlocker-rs compute590 help` (same for `compute90hx-v67` and `compute50hx-v534`).



## Disclaimer

- This tool is only for hardware you own.
- Changing GPU operating parameters may void your hardware warranty.
- The authors accept no liability for hardware damage, data loss or lost revenue. Use at your own risk.

MIT License. See `NOTICE` for third-party components.
