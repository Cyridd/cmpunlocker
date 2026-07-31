# cmpunlocker

中文 | [English](USAGE.en.md)

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
lspci -Dnn | grep -i nvidia
modinfo -F version nvidia
uname -r
```

---



# 1. 临时解锁

重启后失效，重跑一次即可；想恢复原状直接重启。



## 1.1 下载二进制

只需要下载这一次，下面所有型号共用。

```sh
cd /var/tmp
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/cmpunlocker-v0.1.23-linux-x64-cli.tar.gz \
&& wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/SHA256SUMS \
&& sha256sum -c SHA256SUMS --ignore-missing \
&& tar vxzf cmpunlocker-v0.1.23-linux-x64-cli.tar.gz \
&& cd cmpunlocker-v0.1.23-linux-x64-cli \
&& ./cmpunlocker-rs --version
```



## 1.2 运行解锁命令

在上面解压出来的目录里执行，按你的卡选一段。



#### CMP 170HX（驱动 590.48.01 / 595.71.05）

```sh
sudo ./cmpunlocker-rs compute590 run \
--all-cmp170hx \
--acknowledge I-ACCEPT-590-FLEET-COMPUTE-TRANSACTION \
--quiesce
```

成功标志：`RESULT=success_compute_active`



#### CMP 170HX（驱动 610.43.03）

必须多带 `--profile 610.43.03-compute`，不带只会做只读检查，什么都不改。

```sh
sudo ./cmpunlocker-rs compute590 run \
--profile 610.43.03-compute \
--all-cmp170hx \
--acknowledge I-ACCEPT-590-FLEET-COMPUTE-TRANSACTION \
--quiesce
```

成功标志：`RESULT=success_compute_active`



#### CMP 90HX

```sh
sudo ./cmpunlocker-rs compute90hx-v67 run \
--all-cmp90hx \
--acknowledge I-ACCEPT-90HX-V67-COMPUTE-UNLOCK \
&& sudo ./cmpunlocker-rs compute90hx-v67 verify --all-cmp90hx --expect full
```

成功标志：`PASS_CMP90HX_ALL_TARGETS_FULL_SPEED`



---



# 2. 持久解锁

重启后仍然生效。需要在目标机**现场编译**内核模块，用的是另外两个专用包。
换内核或换驱动后必须重新编译安装。

前置依赖：`/lib/modules/$(uname -r)/build`、`make`、`gcc`、`patch`，并关闭 Secure Boot。

CMP 90HX 目前只有临时解锁，没有持久方案。



## 2.1 CMP 50HX 持久解锁

环境：NVIDIA Open `580.173.02` 或 `610.43.03` + 内核 `6.1.0-hiveos`。同时支持 OEM 卡 `1462:371f`。

#### **第一步，下载并编译**，按你的驱动版本选一段。

驱动 `610.43.03`：

```sh
cd /var/tmp
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/cmpunlocker-v0.1.23-linux-x64-50hx-stockflow.tar.gz \
&& tar vxzf cmpunlocker-v0.1.23-linux-x64-50hx-stockflow.tar.gz \
&& cd cmpunlocker-v0.1.23-linux-x64-50hx-stockflow \
&& wget -c https://download.nvidia.com/XFree86/NVIDIA-kernel-module-source/NVIDIA-kernel-module-source-610.43.03.tar.xz \
&& cd stockflow/610.43.03 \
&& ./build-candidate.sh --source-tarball ../../NVIDIA-kernel-module-source-610.43.03.tar.xz \
&& cd ../..
```

驱动 `580.173.02`：

```sh
cd /var/tmp
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/cmpunlocker-v0.1.23-linux-x64-50hx-stockflow.tar.gz \
&& tar vxzf cmpunlocker-v0.1.23-linux-x64-50hx-stockflow.tar.gz \
&& cd cmpunlocker-v0.1.23-linux-x64-50hx-stockflow \
&& wget -c https://download.nvidia.com/XFree86/NVIDIA-kernel-module-source/NVIDIA-kernel-module-source-580.173.02.tar.xz \
&& cd stockflow/580.173.02 \
&& ./build-candidate.sh --source-tarball ../../NVIDIA-kernel-module-source-580.173.02.tar.xz \
&& cd ../..
```



#### **第二步，先探测再安装**。

`stockflow-probe` 会临时加载验证能否达到 full-speed，验完自动恢复原驱动；通过后才会继续安装。安装时会打印 `BACKUP_DIR`，记下来备用。

```sh
ART="stockflow/$(modinfo -F version nvidia)/artifacts/$(modinfo -F version nvidia)-$(uname -r)-v551-stockflow"

sudo ./cmpunlocker-rs compute50hx-v534 stockflow-probe \
--all-cmp50hx \
--stockflow-candidate "$ART" \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK \
&& sudo ./cmpunlocker-rs compute50hx-v534 stockflow-install \
--stockflow-candidate "$ART" \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK

sudo reboot
```



#### **第三步，重启后验证**：

```sh
cd /var/tmp/cmpunlocker-v0.1.23-linux-x64-50hx-stockflow
sudo ./cmpunlocker-rs compute50hx-v534 verify --all-cmp50hx --expect full
```

成功标志：`PASS_CMP50HX_ALL_TARGETS_FULL_SPEED`



#### **恢复原状**：

```sh
sudo ./cmpunlocker-rs compute50hx-v534 stockflow-restore \
--backup-dir <安装时打印的 BACKUP_DIR> \
--acknowledge I-ACCEPT-50HX-V534-COMPUTE-UNLOCK
```



## 2.2 CMP 170HX 持久解锁

把每张卡的可见显存从 8 GiB 变成 64 GiB，重启后仍生效。

环境：**只支持 stock `610.43.03` open kernel module**，必须在目标机现场编译，不能复用别的机器编好的 `.ko`。



#### **第一步，把驱动换成 stock 610.43.03**（HiveOS 示例）：

```sh
cd /var/tmp
wget -c https://download.nvidia.com/XFree86/Linux-x86_64/610.43.03/NVIDIA-Linux-x86_64-610.43.03.run \
&& /hive/sbin/nvidia-driver-update /var/tmp/NVIDIA-Linux-x86_64-610.43.03.run
```



#### **第二步，下载并解压到普通用户目录**。

**编译必须用普通用户跑，用 root 会被拒绝：**

```sh
id cmpbuild >/dev/null 2>&1 || useradd -m -s /bin/bash cmpbuild
cd /home/cmpbuild
wget -c https://github.com/pearlfortune/cmpunlocker/releases/download/v0.1.23/cmpunlocker-v0.1.23-linux-x64-170hx-64g.tar.gz \
&& tar vxzf cmpunlocker-v0.1.23-linux-x64-170hx-64g.tar.gz \
&& chown -R cmpbuild:cmpbuild /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g
```

包内已自带 NVIDIA 官方 610.43.03 open kernel 源码，不用另外下载。



#### **第三步，编译（普通用户）+ 安装（root）+ 重启**：

```sh
su -s /bin/bash cmpbuild -c '
cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g
./build.sh --all-supported-cmp170hx \
--acknowledge I-ACCEPT-UNVERIFIED-610-MEMORY-KERNEL-BUILD'

cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g
sudo ./install.sh --all-supported-cmp170hx \
--acknowledge I-ACCEPT-UNVERIFIED-610-MEMORY-KERNEL-INSTALL

sudo reboot
```



#### **第四步，重启后验证**：

```sh
nvidia-smi           # 每张卡应显示 65536 MiB
modinfo -n nvidia    # 应指向 updates/cmpunlocker-610-memory/nvidia.ko
```

成功标志：`65536 MiB`



#### **恢复原状**

分两阶段。第一阶段只移除模块目录并重建 initramfs：

```sh
cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g
sudo ./remove.sh --acknowledge REMOVE-CMPUNLOCKER-610-MEMORY-WITHOUT-HOT-UNLOAD
```



然后关机、拔 AC 电、冷启动，再跑第二阶段确认：

```sh
cd /home/cmpbuild/cmpunlocker-v0.1.23-linux-x64-170hx-64g
sudo ./remove.sh --confirm-cold-cycle \
--acknowledge I-CONFIRM-FULL-AC-POWER-CYCLE-AFTER-610-MEMORY-REMOVAL
```

---



## 说明

- 单卡解锁：把 `--all-cmp170hx` / `--all-cmp90hx` / `--all-cmp50hx` 换成 `--target-bdf 0000:01:00.0`。
- 运行时会卸载并重新加载 NVIDIA 驱动、停掉 miner，别在生产窗口跑。
- 出错时看 `/var/lib/cmpunlocker-rs/transactions/` 下的 JSON 报告，里面有完整失败原因。
- 完整参数：`./cmpunlocker-rs compute590 help`（`compute90hx-v67` / `compute50hx-v534` 同理）。



## 免责声明

- 本工具仅供在你自己拥有的硬件上使用。
- 修改 GPU 运行参数可能导致硬件失去保修。
- 作者不对任何硬件损坏、数据丢失或收益损失承担责任，使用风险自负。

MIT License。第三方组件声明见 `NOTICE`。
