# cmpunlocker

[中文](README.md) | English

Github: https://github.com/pearlfortune/cmpunlocker

Unlocks the compute limiter on NVIDIA CMP 170HX / 90HX cards you own.
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
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/cmpunlocker-v0.1.23-linux-x64-cli.tar.gz

# Download the checksum file
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/SHA256SUMS

# Verify the download; you must see OK. Anything else means an incomplete file - delete and retry
sha256sum -c SHA256SUMS --ignore-missing

# Extract
tar vxzf cmpunlocker-v0.1.23-linux-x64-cli.tar.gz

# Enter the extracted directory; every later command runs from here
cd cmpunlocker-v0.1.23-linux-x64-cli

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

---



# 2. Persistent unlock

Survives reboots. Kernel modules must be **built on the target host**, using a
separate bundle. You must rebuild and reinstall after any kernel or driver change.

Requirements: `/lib/modules/$(uname -r)/build`, `make`, `gcc`, `patch`, and Secure Boot off.

CMP 90HX currently has no persistent path — temporary unlock only.



## 2.1 CMP 170HX persistent VRAM unlock

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
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/cmpunlocker-v0.1.23-linux-x64-170hx-64g.tar.gz

# Extract
tar vxzf cmpunlocker-v0.1.23-linux-x64-170hx-64g.tar.gz

# Hand the directory to the build user, otherwise the next step cannot write to it
chown -R cmpbuild:cmpbuild /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g
```

The bundle already ships NVIDIA's official 610.43.03 open kernel source, so nothing else needs downloading.



#### **Step 3 — build (normal user), install (root), reboot**:

```sh
# Build the kernel modules as the normal user; takes a few minutes
su -s /bin/bash cmpbuild -c '
cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g
./build.sh --all-supported-cmp170hx \
--acknowledge I-ACCEPT-UNVERIFIED-610-MEMORY-KERNEL-BUILD'

cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g

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
cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g

# Stage one: remove the modules
sudo ./remove.sh --acknowledge REMOVE-CMPUNLOCKER-610-MEMORY-WITHOUT-HOT-UNLOAD
```



Then shut down, pull AC power, cold boot, and run the second stage to confirm:

```sh
cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g

# Stage two: confirm the card is back to stock after the cold boot
sudo ./remove.sh --confirm-cold-cycle \
--acknowledge I-CONFIRM-FULL-AC-POWER-CYCLE-AFTER-610-MEMORY-REMOVAL
```

---



## Notes

- Single card: replace `--all-cmp170hx` / `--all-cmp90hx` with `--target-bdf 0000:01:00.0`, using the BDF from the `lspci` output above.
- Running it unloads and reloads the NVIDIA driver and stops your miner — do not run it during a production window.
- On failure, read the JSON transaction report under `/var/lib/cmpunlocker-rs/transactions/`; it contains the full reason.
- Full flags: `./cmpunlocker-rs compute590 help` (same for `compute90hx-v67`).



## Disclaimer

- This tool is only for hardware you own.
- Changing GPU operating parameters may void your hardware warranty.
- The authors accept no liability for hardware damage, data loss or lost revenue. Use at your own risk.

MIT License. See `NOTICE` for third-party components.
