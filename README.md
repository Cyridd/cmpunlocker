# cmpunlocker

## 1. 170hx 临时解锁算力 (重启后失效)

注意：HiveOS 用户，解锁后需要执行以下命令重启 Hive 服务，否则 Web 页面会显示掉卡
```sh
systemctl restart hive.service
```

适用版本：

- `590.48.01` / `595.71.05`：默认 `auto` profile 可执行。
- `610.43.03`：必须显式加 `--profile 610.43.03-compute`。

它只解锁算力，不解锁 64G；驱动重载或重启后需要重新执行。

先做只读检查：

```bash
# 后续命令统一使用这个路径；如果这里不存在，先回到第 1 步安装 CLI。
BIN=/opt/cmpunlocker-rs/bin/cmpunlocker-rs

# 只读检查，不写 BAR0，不改固件，不卸载/加载驱动。
"$BIN" compute590 status \
  --transaction-dir /var/lib/cmpunlocker-rs/transactions \
  --all-cmp170hx
```

`590.48.01` / `595.71.05` 临时解锁：

```bash
# 590/595 默认 auto profile 会自动匹配已验证版本。
"$BIN" compute590 run \
  --transaction-dir /var/lib/cmpunlocker-rs/transactions \
  --acknowledge I-ACCEPT-590-FLEET-COMPUTE-TRANSACTION \
  --all-cmp170hx \
  --quiesce \
  --benchmark none
```

`610.43.03` 临时解锁：

```bash
# 610 必须显式指定 compute profile；不加这一行会走只读 profile。
"$BIN" compute590 run \
  --profile 610.43.03-compute \
  --transaction-dir /var/lib/cmpunlocker-rs/transactions \
  --acknowledge I-ACCEPT-590-FLEET-COMPUTE-TRANSACTION \
  --all-cmp170hx \
  --quiesce \
  --benchmark none
```

成功标准：最后显示 `RESULT=success_compute_active`，并且状态报告能读回
`FEAT_OVR_PLM=0xffffffff`、`FEAT_OVR_SM_SPD=0x88888888`、
`FEAT_OVR_SM_SPD_1=0x00000008`。8021/hive170hx-003 的 610 实际算力测试已确认通过，
未记录具体 T/s 数值。
