# cmpunlocker

中文 | [English](README.en.md)

Github: https://github.com/pearlfortune/cmpunlocker

解锁自有 NVIDIA CMP 170HX / 90HX / 50HX 的算力限制。Linux x86-64，需要 root。

不刷 VBIOS，刷不坏卡。驱动 / 内核 / VBIOS 不在下表内时，程序直接拒绝执行，不会写入。



## 已测试环境

| 型号 | PCI ID | 驱动 | 内核 | VBIOS |
| --- | --- | --- | --- | --- |
| CMP 170HX | `10de:20c2` | `590.48.01` / `595.71.05` | 不限 | 不限 |
| CMP 170HX | `10de:20c2` | `610.43.03` | 不限 | 不限 |
| CMP 90HX | `10de:220d` | Open `580.159.03` | `6.10.0-hiveos` | `94.02.74.00.01` |
| CMP 90HX | `10de:220d` | Open `610.43.03` | `6.10.0-hiveos` | `94.02.74.00.01` / `94.02.74.00.05` |
| CMP 50HX | `10de:1e09` | Open `580.159.03` | `6.8.0-136-generic` | 不限 |
| CMP 50HX | `10de:1e09` | Open `580.173.02` | `6.1.0-hiveos` | 不限 |
| CMP 50HX | `10de:1e09` | Open `610.43.03`（持久 stockflow） | `6.1.0-hiveos` | 不限 |

查看自己的环境：

```sh
# 看有哪些 NVIDIA 卡，记下 PCI ID 和 BDF（形如 0000:01:00.0）
lspci -Dnn | grep -i nvidia

# 当前 NVIDIA 驱动版本
modinfo -F version nvidia

# 当前内核版本
uname -r
```

---

# 1. 临时解锁

重启后失效，重跑一次即可；想恢复原状直接重启。



## 1.1 下载二进制

只需要下载这一次，下面所有型号共用。**一条一条执行**，方便看出哪一步失败。

```sh
# 进入临时目录
cd /var/tmp

# 下载二进制包
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.25/cmpunlocker-v0.1.25-linux-x64-cli.tar.gz

# 下载校验文件
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.25/SHA256SUMS

# 校验完整性，必须看到 OK；不 OK 就是没下全，删掉重下
sha256sum -c SHA256SUMS --ignore-missing

# 解压
tar vxzf cmpunlocker-v0.1.25-linux-x64-cli.tar.gz

# 进入解压出来的目录，后面所有命令都在这里执行
cd cmpunlocker-v0.1.25-linux-x64-cli

# 确认能跑起来，会打印版本号
./cmpunlocker-rs --version
```



## 1.2 运行解锁命令

在上面解压出来的目录里执行，按你的卡选一段。



#### CMP 170HX（驱动 590.48.01 / 595.71.05）

```sh
# 解锁全部 170HX。--quiesce 会先停掉占用 GPU 的 miner / watchdog / Xorg
sudo ./cmpunlocker-rs compute590 run \
--all-cmp170hx \
--acknowledge I-ACCEPT-590-FLEET-COMPUTE-TRANSACTION \
--quiesce
```

成功标志：`RESULT=success_compute_active`



#### CMP 170HX（驱动 610.43.03）

必须多带 `--profile 610.43.03-compute`，不带只会做只读检查，什么都不改。

```sh
# 610 驱动必须显式指定 compute profile
sudo ./cmpunlocker-rs compute590 run \
--profile 610.43.03-compute \
--all-cmp170hx \
--acknowledge I-ACCEPT-590-FLEET-COMPUTE-TRANSACTION \
--quiesce
```

成功标志：`RESULT=success_compute_active`



#### CMP 90HX

```sh
# 解锁全部 90HX
sudo ./cmpunlocker-rs compute90hx-v67 run \
--all-cmp90hx \
--acknowledge I-ACCEPT-90HX-V67-COMPUTE-UNLOCK

# 复查状态，这一步是只读的，随时可以单独跑
sudo ./cmpunlocker-rs compute90hx-v67 verify --all-cmp90hx --expect full
```

成功标志：`PASS_CMP90HX_ALL_TARGETS_FULL_SPEED`



#### CMP 50HX

用的是上面同一个 `cmpunlocker-rs` 二进制，不用另外下载。程序会按当前驱动 / 内核自动选内嵌方案（`580.159.03` + `6.8.0-136-generic` 或 `580.173.02` + `6.1.0-hiveos`）。

```sh
# 先做只读预检
sudo ./cmpunlocker-rs compute50hx-v534 preflight --all-cmp50hx

# 解锁全部 50HX。--probe-unsupported-subsystems 会顺带探测 OEM 卡，但只激活通过 full-speed 门的卡
sudo ./cmpunlocker-rs compute50hx-v534 run \
--all-cmp50hx \
--probe-unsupported-subsystems \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK

# 复查状态，这一步是只读的，随时可以单独跑
sudo ./cmpunlocker-rs compute50hx-v534 verify --all-cmp50hx --expect full
```

成功标志：`PASS_CMP50HX_ALL_TARGETS_V534_HANDOFF_FULL_SPEED`，`verify` 后 `PASS_CMP50HX_ALL_TARGETS_FULL_SPEED`。

OEM / ID=4 卡（subsystem `1462:371f`）这条 V534 路径激活不了，走下面的 **2.3 CMP 50HX 持久算力解锁**。运行会停掉 miner / watchdog，别在生产窗口跑。

---



# 2. 持久解锁

重启后仍然生效。需要在目标机**现场编译**内核模块，用的是单独的专用包。
换内核或换驱动后必须重新编译安装。

前置依赖：`/lib/modules/$(uname -r)/build`、`make`、`gcc`、`patch`、`binutils`，并关闭 Secure Boot。



## 2.1 CMP 90HX 持久算力解锁

CMP 90HX 的持久算力当前是 **610.43.03 工程预览**。已在 8021/hive2222 单卡
`94.02.74.00.01` 上通过 3 次重启验证，8024/xinxitong 已验证现成 artifact 直接安装后
full-speed；不创建 systemd 服务。换内核、换驱动、`94.02.74.00.05` 或多卡持久化仍需
重新验证。

环境：**只支持 NVIDIA Open `610.43.03` + kernel `6.10.0-hiveos` + CMP 90HX
`10de:220d` / `10de:1555` + VBIOS `94.02.74.00.01`**。



#### **第一步，确认当前环境**

```sh
# 当前内核必须是 6.10.0-hiveos
uname -r

# 当前 NVIDIA 驱动必须是 610.43.03
modinfo -F version nvidia

# 必须是 open kernel module；通常会看到 Dual MIT/GPL
modinfo -F license nvidia

# 确认能看到 CMP 90HX
nvidia-smi -L
```



#### **第二步，下载并校验 90HX stockflow 包**

```sh
VERSION=v0.1.25
ASSET="cmpunlocker-${VERSION}-linux-x64-90hx-stockflow"
BASE="https://github.com/pearlfortune/cmpunlocker/releases/download/${VERSION}"

cd /var/tmp

# 下载 90HX 持久解锁包
wget -c "${BASE}/${ASSET}.tar.gz"

# 下载校验文件并校验，必须看到 OK
wget -c "${BASE}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing

# 解压并进入 90HX stockflow 目录
tar vxzf "${ASSET}.tar.gz"
cd "${ASSET}/stockflow/610.43.03"
```



#### **第三步，准备 NVIDIA 官方源码并构建 artifact**

```sh
# 准备 NVIDIA 官方 open kernel source；也可以替换成你已经下载好的本地路径
wget -c https://download.nvidia.com/XFree86/NVIDIA-kernel-module-source/NVIDIA-kernel-module-source-610.43.03.tar.xz
SOURCE="${PWD}/NVIDIA-kernel-module-source-610.43.03.tar.xz"

# 构建已验证的 rejoin13-open-retry artifact
CMP90_STOCKFLOW_VARIANT=rejoin13 ./build-candidate.sh --source-tarball "${SOURCE}"
ART="artifacts/610.43.03-$(uname -r)-rejoin13-open-retry"

# 确认 artifact 和当前驱动、内核匹配
modinfo -F version "${ART}/nvidia.ko"
modinfo -F vermagic "${ART}/nvidia.ko"
strings "${ART}/nvidia.ko" | grep -E 'CMP90_STOCKFLOW_REJOIN12|CMP90_STOCKFLOW_REJOIN13'
```



#### **第四步，安装并重启**

```sh
# 安装到隔离 updates 目录；这一步不热卸载驱动，成功后重启
sudo ./stockflow-install.sh \
--artifact "${ART}" \
--acknowledge I-ACCEPT-90HX-STOCKFLOW-PERSISTENT-INSTALL
sudo reboot
```



#### **第五步，重启后验证**

```sh
cd /var/tmp/cmpunlocker-v0.1.25-linux-x64-90hx-stockflow/stockflow/610.43.03

# 重启后只读复查
BIN=../../cmpunlocker-rs
sudo "$BIN" compute90hx-v67 verify --all-cmp90hx --expect full

# 确认当前加载路径指向 stockflow 目录
modinfo -n nvidia
```

成功标志：`PASS_CMP90HX_FULL_SPEED` 或 `PASS_CMP90HX_ALL_TARGETS_FULL_SPEED`。

v0.1.25 起，安装脚本会写 `/etc/depmod.d/cmpunlocker-90hx-stockflow.conf`，确保重启时优先加载
`updates/cmpunlocker-90hx-stockflow` 里的模块，而不是 DKMS stock 模块。重复执行安装命令如果返回
`PASS_CMP90HX_STOCKFLOW_ALREADY_INSTALLED`，说明当前已经是持久 stockflow 解析路径。



#### **恢复 stock 解析路径**

```sh
cd /var/tmp/cmpunlocker-v0.1.25-linux-x64-90hx-stockflow/stockflow/610.43.03

# 恢复脚本只移除持久模块解析路径，不热卸载当前驱动；执行后重启
sudo ./stockflow-restore.sh --acknowledge I-ACCEPT-90HX-STOCKFLOW-RESTORE
sudo reboot

# 重启后确认回到 stock 模块
modinfo -n nvidia

# 可选：确认已经回到 locked 状态
cd /var/tmp/cmpunlocker-v0.1.25-linux-x64-90hx-stockflow/stockflow/610.43.03
BIN=../../cmpunlocker-rs
sudo "$BIN" compute90hx-v67 verify --all-cmp90hx --expect locked
```



## 2.2 CMP 170HX 持久显存解锁

解锁被限制的可见显存容量，重启后仍生效。实测机器上每张卡从 `8192 MiB` 提升到
`65536 MiB`；实际容量以你自己的卡为准。

环境：**只支持 stock `610.43.03` open kernel module**，必须在目标机现场编译，不能复用别的机器编好的 `.ko`。



#### **第一步，把驱动换成 stock 610.43.03**（HiveOS 示例）：

```sh
cd /var/tmp

# 下载 NVIDIA 官方驱动 runfile
wget -c https://download.nvidia.com/XFree86/Linux-x86_64/610.43.03/NVIDIA-Linux-x86_64-610.43.03.run

# 用 HiveOS 自带工具升级驱动；非 HiveOS 用你系统自己的驱动安装方式
/hive/sbin/nvidia-driver-update /var/tmp/NVIDIA-Linux-x86_64-610.43.03.run

# 升级完确认版本是 610.43.03
modinfo -F version nvidia
```



#### **第二步，下载并解压到普通用户目录**。

**编译必须用普通用户跑，用 root 会被拒绝：**

```sh
# 建一个专门用来编译的普通用户（已存在就跳过）
id cmpbuild >/dev/null 2>&1 || useradd -m -s /bin/bash cmpbuild

cd /home/cmpbuild

# 下载显存解锁专用包
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.25/cmpunlocker-v0.1.25-linux-x64-170hx-64g.tar.gz

# 解压
tar vxzf cmpunlocker-v0.1.25-linux-x64-170hx-64g.tar.gz

# 把目录交给编译用户，否则下一步没有写权限
chown -R cmpbuild:cmpbuild /home/cmpbuild/cmpunlocker-v0.1.25-linux-x64-170hx-64g
```

包内已自带 NVIDIA 官方 610.43.03 open kernel 源码，不用另外下载。



#### **第三步，编译（普通用户）+ 安装（root）+ 重启**：

```sh
# 用普通用户编译内核模块，耗时几分钟
su -s /bin/bash cmpbuild -c '
cd /home/cmpbuild/cmpunlocker-v0.1.25-linux-x64-170hx-64g
./build.sh --all-supported-cmp170hx \
--acknowledge I-ACCEPT-UNVERIFIED-610-MEMORY-KERNEL-BUILD'

cd /home/cmpbuild/cmpunlocker-v0.1.25-linux-x64-170hx-64g

# 用 root 安装模块，会写 /lib/modules 并更新 initramfs
sudo ./install.sh --all-supported-cmp170hx \
--acknowledge I-ACCEPT-UNVERIFIED-610-MEMORY-KERNEL-INSTALL

# 必须重启才生效
sudo reboot
```



#### **第四步，重启后验证**：

```sh
# 每张卡的显存容量应该变大
nvidia-smi

# 确认加载的是解锁后的模块，不是原厂模块
modinfo -n nvidia
```

成功标志：`nvidia-smi` 显示的显存容量高于解锁前，且 `modinfo` 输出里包含
`updates/cmpunlocker-610-memory/nvidia.ko`。



#### **恢复原状**

分两阶段。第一阶段只移除模块目录并重建 initramfs，不会热卸载当前驱动：

```sh
cd /home/cmpbuild/cmpunlocker-v0.1.25-linux-x64-170hx-64g

# 第一阶段：移除模块
sudo ./remove.sh --acknowledge REMOVE-CMPUNLOCKER-610-MEMORY-WITHOUT-HOT-UNLOAD
```



然后关机、拔 AC 电、冷启动，再跑第二阶段确认：

```sh
cd /home/cmpbuild/cmpunlocker-v0.1.25-linux-x64-170hx-64g

# 第二阶段：冷启动后确认已回到原厂状态
sudo ./remove.sh --confirm-cold-cycle \
--acknowledge I-CONFIRM-FULL-AC-POWER-CYCLE-AFTER-610-MEMORY-REMOVAL
```

---



## 2.3 CMP 50HX 持久算力解锁

把 50HX 的算力解锁做成 patched open driver，重启后仍然生效，也是唯一能覆盖 OEM / ID=4
卡（subsystem `1462:371f`）的路径。必须在目标机**现场编译**，换内核或换驱动后必须重新编译。

环境：**只支持 NVIDIA Open `580.173.02` 或 `610.43.03` + kernel `6.1.0-hiveos` + CMP 50HX
`10de:1e09`**（subsystem `10de:1554` 或 `1462:371f`）。

前置依赖同上：`/lib/modules/$(uname -r)/build`、`make`、`gcc`、`patch`、`binutils`，并关闭 Secure Boot。



#### **第一步，下载并校验 50HX stockflow 包**

```sh
VERSION=v0.1.25
ASSET="cmpunlocker-${VERSION}-linux-x64-50hx-stockflow"
BASE="https://github.com/pearlfortune/cmpunlocker/releases/download/${VERSION}"

cd /var/tmp

# 下载 50HX 持久解锁包
wget -c "${BASE}/${ASSET}.tar.gz"

# 下载校验文件并校验，必须看到 OK
wget -c "${BASE}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing

# 解压并进入包顶层，包顶层自带 ./cmpunlocker-rs
tar vxzf "${ASSET}.tar.gz"
cd "${ASSET}"
BIN=./cmpunlocker-rs
```



#### **第二步，按当前驱动取官方源码并现场编译 artifact**

```sh
# 按当前 NVIDIA 驱动选择源码和构建目录
DRIVER="$(modinfo -F version nvidia)"
case "${DRIVER}" in
  580.173.02) SOURCE="NVIDIA-kernel-module-source-580.173.02.tar.xz"; WORK="stockflow/580.173.02" ;;
  610.43.03)  SOURCE="NVIDIA-kernel-module-source-610.43.03.tar.xz";  WORK="stockflow/610.43.03"  ;;
  *) echo "unsupported 50HX stockflow driver: ${DRIVER}" >&2; exit 2 ;;
esac

# 下载 NVIDIA 官方 open kernel source（公开地址，目标机可直接下）
wget -c "https://download.nvidia.com/XFree86/NVIDIA-kernel-module-source/${SOURCE}"

# 现场编译 stock-flow artifact，耗时几分钟
cd "${WORK}"
./build-candidate.sh --source-tarball "../../${SOURCE}"
cd ../..

# 编好的 artifact 路径
ART="${WORK}/artifacts/${DRIVER}-$(uname -r)-v551-stockflow"
```



#### **第三步，先非持久探测，再持久安装并重启**

```sh
# 先非持久探测，确认 artifact 能进入 full-speed；成功后会自动恢复原 stock driver
sudo "$BIN" compute50hx-v534 stockflow-probe \
--all-cmp50hx \
--stockflow-candidate "${ART}" \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK

# 持久安装：备份当前模块 + 安装 artifact 里的 5 个 .ko；不自动重启
sudo "$BIN" compute50hx-v534 stockflow-install \
--stockflow-candidate "${ART}" \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK

# 记下上一条命令输出里的 BACKUP_DIR=，恢复时要用，然后重启
sudo reboot
```



#### **第四步，重启后验证**

```sh
cd /var/tmp/cmpunlocker-v0.1.25-linux-x64-50hx-stockflow
BIN=./cmpunlocker-rs

# 确认能看到全部 50HX
nvidia-smi -L

# 只读复查，每张卡都应 full-speed
sudo "$BIN" compute50hx-v534 verify --all-cmp50hx --expect full
```

成功标志：`PASS_CMP50HX_ALL_TARGETS_FULL_SPEED`。换内核或换驱动后必须重新执行第二步的 `build-candidate.sh`。



#### **恢复安装前模块**

用第三步 `stockflow-install` 输出里打印的 `BACKUP_DIR`（形如
`/var/lib/cmpunlocker-rs/transactions/compute50hx-v534-stockflow-install-<时间戳>/installed-module-backup`）：

```sh
cd /var/tmp/cmpunlocker-v0.1.25-linux-x64-50hx-stockflow
BIN=./cmpunlocker-rs

# 恢复安装前的 stock 模块；执行后重启
sudo "$BIN" compute50hx-v534 stockflow-restore \
--backup-dir /var/lib/cmpunlocker-rs/transactions/compute50hx-v534-stockflow-install-<时间戳>/installed-module-backup \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK
sudo reboot
```

---



## 说明

- 单卡解锁：把 `--all-cmp170hx` / `--all-cmp90hx` / `--all-cmp50hx` 换成 `--target-bdf 0000:01:00.0`，BDF 用第一步 `lspci` 查到的。
- 运行时会卸载并重新加载 NVIDIA 驱动、停掉 miner，别在生产窗口跑。
- 出错时看 `/var/lib/cmpunlocker-rs/transactions/` 下的 JSON 报告，里面有完整失败原因。
- 完整参数：`./cmpunlocker-rs compute590 help`（`compute90hx-v67`、`compute50hx-v534` 同理）。



## 免责声明

- 本工具仅供在你自己拥有的硬件上使用。
- 修改 GPU 运行参数可能导致硬件失去保修。
- 作者不对任何硬件损坏、数据丢失或收益损失承担责任，使用风险自负。

MIT License。第三方组件声明见 `NOTICE`。
