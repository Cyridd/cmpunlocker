# cmpunlocker

中文 | [English](README.en.md)

Github: https://github.com/pearlfortune/cmpunlocker

解锁自有 NVIDIA CMP 170HX / 90HX 的算力限制。Linux x86-64，需要 root。

不刷 VBIOS，刷不坏卡。驱动 / 内核 / VBIOS 不在下表内时，程序直接拒绝执行，不会写入。



## 已测试环境

| 型号 | PCI ID | 驱动 | 内核 | VBIOS |
| --- | --- | --- | --- | --- |
| CMP 170HX | `10de:20c2` | `590.48.01` / `595.71.05` | 不限 | 不限 |
| CMP 170HX | `10de:20c2` | `610.43.03` | 不限 | 不限 |
| CMP 90HX | `10de:220d` | Open `580.159.03` | `6.10.0-hiveos` | `94.02.74.00.01` |
| CMP 90HX | `10de:220d` | Open `610.43.03` | `6.10.0-hiveos` | `94.02.74.00.01` / `94.02.74.00.05` |

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
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/cmpunlocker-v0.1.23-linux-x64-cli.tar.gz

# 下载校验文件
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/SHA256SUMS

# 校验完整性，必须看到 OK；不 OK 就是没下全，删掉重下
sha256sum -c SHA256SUMS --ignore-missing

# 解压
tar vxzf cmpunlocker-v0.1.23-linux-x64-cli.tar.gz

# 进入解压出来的目录，后面所有命令都在这里执行
cd cmpunlocker-v0.1.23-linux-x64-cli

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

---



# 2. 持久解锁

重启后仍然生效。需要在目标机**现场编译**内核模块，用的是单独的专用包。
换内核或换驱动后必须重新编译安装。

前置依赖：`/lib/modules/$(uname -r)/build`、`make`、`gcc`、`patch`，并关闭 Secure Boot。

CMP 90HX 目前只有临时解锁，没有持久方案。



## 2.1 CMP 170HX 持久显存解锁

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
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/cmpunlocker-v0.1.23-linux-x64-170hx-64g.tar.gz

# 解压
tar vxzf cmpunlocker-v0.1.23-linux-x64-170hx-64g.tar.gz

# 把目录交给编译用户，否则下一步没有写权限
chown -R cmpbuild:cmpbuild /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g
```

包内已自带 NVIDIA 官方 610.43.03 open kernel 源码，不用另外下载。



#### **第三步，编译（普通用户）+ 安装（root）+ 重启**：

```sh
# 用普通用户编译内核模块，耗时几分钟
su -s /bin/bash cmpbuild -c '
cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g
./build.sh --all-supported-cmp170hx \
--acknowledge I-ACCEPT-UNVERIFIED-610-MEMORY-KERNEL-BUILD'

cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g

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
cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g

# 第一阶段：移除模块
sudo ./remove.sh --acknowledge REMOVE-CMPUNLOCKER-610-MEMORY-WITHOUT-HOT-UNLOAD
```



然后关机、拔 AC 电、冷启动，再跑第二阶段确认：

```sh
cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g

# 第二阶段：冷启动后确认已回到原厂状态
sudo ./remove.sh --confirm-cold-cycle \
--acknowledge I-CONFIRM-FULL-AC-POWER-CYCLE-AFTER-610-MEMORY-REMOVAL
```

---



## 说明

- 单卡解锁：把 `--all-cmp170hx` / `--all-cmp90hx` 换成 `--target-bdf 0000:01:00.0`，BDF 用第一步 `lspci` 查到的。
- 运行时会卸载并重新加载 NVIDIA 驱动、停掉 miner，别在生产窗口跑。
- 出错时看 `/var/lib/cmpunlocker-rs/transactions/` 下的 JSON 报告，里面有完整失败原因。
- 完整参数：`./cmpunlocker-rs compute590 help`（`compute90hx-v67` 同理）。



## 免责声明

- 本工具仅供在你自己拥有的硬件上使用。
- 修改 GPU 运行参数可能导致硬件失去保修。
- 作者不对任何硬件损坏、数据丢失或收益损失承担责任，使用风险自负。

MIT License。第三方组件声明见 `NOTICE`。
