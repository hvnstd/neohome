# NeoHome — 转接交接文档（HANDOFF）

本文档给接手的 AI/开发者：先读完 §1–§3，再按 §4 的顺序动代码。
规格唯一权威来源是 `PROJECT.md`；文件所有权看 `WORKSTREAM_NOTES.md`；
当前完成态看 `PROGRESS.md`；AGENTS.md 末尾是一整块 gotchas，动对应
子系统前必读。

## 1. 项目是什么

NeoHome = 一个持续运行的 Go 世界模拟器：家庭网络 + VPS + 互联网
基础设施 + Linux/Unix 全套命令，玩家用 shell/SSH/telnet/浏览器进入，
黑客/防守/取证/执法全是同一套真实状态机。硬约束（永远遵守）：

- 不执行真实宿主机代码 / 不跑真 ELF / 不真 Docker / 不给每玩家真 VM
- 所有状态在 `internal/core`，所有命令在 `internal/shell`，单向依赖
- 每个命令必须读写真实世界状态，无装饰
- 时间只走 tick（`World.Tick()`，30 sim-s），禁止墙钟等待

## 2. 当前状态（2026-10-17）

- 分支:`arena/01a10c9c-neohome`，HEAD `4629dbc`，工作区干净，已推
- `go build` ✓ `go vet ./...` ✓ `go test ./...` **381 pass 0 fail** `gofmt -l` 空
- 阶段：Phase 0/1/2/3 服务端全部完成；真浏览器前端（§45 Web 后续）已落地（`internal/webtty`）
- 入口：telnet `:2024` + SSH `:2222`，存档 `world.gob`，引擎 3s 一 tick，每 12 tick 存盘
- 存档已修：`Device.GobEncode` 现在写 `Rsrc`；`VM.GobEncode` 只带数据（Host/W 重链接）；`WebSessions`/`Households`/`BCStates`/`GroupIDs` 全 round-trip

## 3. 剩余缺口（按优先级）

PROGRESS.md 末尾的“缺口清单”是权威版，摘要：

1. ~~真 3-way git merge~~ **已完成**（merge commit + 递归合并）
2. 数据库高级语义：GRANT/JOIN/事务 已落地；复制仍缺
3. `.py` 前端（bytecode 脚本糖；PostInst 已改诚实文案）
4. §21 LLM 唤醒调度（规则层即 agent 层；MCP 只是透传）
5. §29 更多虚拟漏洞（现 3 个）
6. 随机内容生长（生成器只出住宅，4 lot 封顶）
7. 多家庭子网（LAN 规划变更，addr.go 管）

明确不适用（勿重开）：suid 建模、SQLite 后端。

## 4. 动手前的规约

- 读 `AGENTS.md` §Gotchas 对应子系统的条目（特别是 addr.go 单一所有者、
  `VFS.Read` 目录返回 nil+true、`MkdirAll` 根 bail、`WriteGuest` 单门、
  `Device.FW()` 每次重读、`uci` 两段提交、镜像 derived state、CPU share
  是唯一乘数、`Logf` 满盘丢行、`VFS.Append` 不创建文件……）
- 文件所有权：`WORKSTREAM_NOTES.md` 每节 owns its files；动 cross-cutting
  文件（`world.go` 指针、`engine.go` tick 调用、`world_init.go` seed、
  `resolver.go` backcompat）前先查
- `core` 永不 import `shell`；跨层用注册（如 `SetCronExec`、`SetWebExec`）
- 新字段必须 gob-encodable（无 channel/func），加到 `Device.GobEncode` 时
  三处全写：shadow struct 字段、literal 赋值、`GobDecode` 回填
- 测试：`core.NewWorld()` + `shell.NewShell(...)` + `ExecLine`/`ExecLineStatus`
  或 `runWithStdin`；时间用 `Tick()` 推进，禁墙钟
- 每个新命令/服务必须读写真实世界状态，异常必须有可追溯因

## 5. 目录速查

```
cmd/neohome/         telnet :2024 + SSH :2222 + engine tick/save
internal/core/       世界状态机：world.go / engine.go / world_init.go / resolver.go
                     + 按子系统分文件（mail/cron/vm/tls/bbs/git/iot/smb/sms/
                       usb/ftp/firewall/uci/addrs/agents/attacker/abuse/case/
                       law/intrusion/vuln/resources/vmdisk/link/power/vps/
                       work/vfs/pkg*.go/wan/addr/addrs/bbs/git/backup/
                       hids/security/secaudit/smb/sftp/tls/usb/sms/iot/
                       market/org/players/users/vlan/bgp/db/bytecode/
                       webtty/worldgen/npcmem/market.go/...)
internal/shell/      builtins，按域分文件：fs/game/house/log/net/net_wan/
                     pkg/cron/remote/sec/sftp/smb/sms/ssh?/svc/telnet/
                     text/tls/tmux/usb/vm/world/market/org/bc/build/
                     map_cmds/webtty_cmds/mysql_cmds
tests/               唯一测试包（world_test.go 有 run/runWithStdin 辅助）
tools/               18 个 live verify 脚本 + sshdrive
```

## 6. 验证命令

```bash
go build ./...          # 入口编译
go vet ./...            # 静态检查
go test ./...           # 全量（~220s，381 test）
go test ./tests -run TestName -v   # 单测
gofmt -l .             # 必须空
```

Live 端到端:`tools/all_verify.sh`（18 脚本，占 2024/2222）。

## 7. 关键 gotchas（AGENTS.md 完整版）

- `tools/*_verify.sh` 自定位 checkout，新脚本必须沿用头部
- LAN 数字唯一所有者 `internal/core/addr.go`：别处禁止 `10.77.1.x` 字面量
- `ftp` 是 builtin，**不是** busybox applet
- `ssh`/`telnet` 共用 `openRemoteSession`，nested shell 共用 `bufio.Reader`
- `routerFor(dev)==dev` 不是网关：包发往 household edge 时需 fallback `r=dst`
- `Device.FW()` 每次重读文件，永不清缓存
- `uci set/add/delete` 只 stage，`commit` 才写；reload 是 check verb
- `VFS.Read` 目录返回 `(nil,true)`；`MkdirAll` 相对路径已 bail
- Mirror state derived，不手写；cron 每 15 sim-min 跑 `mirror-sync <tree>`
- `Device.InstalledFrom` 是 per-device，从 own sources 解析
- `WPort==0` redirect = DMZ 全端口
- `Powered()` 顺序：PoE → 电池/盖 → mains；数据机房 NetUp 即 power
- VPS 是 Device：lifecycle 改设备，不是打印
- `VFS.Append` 不创建文件；`VFS.Write` 覆盖；`WriteGuest` 是单门
- 资源 mostly derived；`Rsrc` 只存不可推导量；`ResourceTick` 最后跑、暗机不跑
- `CPUShare()` 是唯一乘数；`WorkRate` = min(NetMbps,DiskMBps)/500 × share
- RAM→swap→OOM；满盘 latch + drop count；安装查 room 返 PackageError
- §33 工具是装出来的软件；ban 在 Dial 前；IDS 读 flow 表；rkhunter/aide/auditd 重读配置
- restic 仓库内容寻址，1 MiB 单 blob；写全走 `mkdir`/`put`
- 文本处理（tokenize/printf/echo -e）是 §33 的底座
- `dd` 64MiB 单文件上限；`stress` 后台 load；进程限 RAMMB/8
- §34 desk findings 只来自 AS 内主机；console 是 Unix group；whois 停在 abuse contact
- §35 lawful request 不由 enforcement 回答；hours 账本；registry 不是目标
- `mail send` 必须 `SendMailFrom(s.Dev,...)`；mbox 格式
- §36 入侵是 state：process+payload+cron.d 行，cron.d 行是持久化
- rDNS 按地址发布，私网/CGNAT/ULA 无 PTR，不指人
- NATed 不 round-trip（已知 bug， noted）
- `Household`/`Org`/`Market`/`Clans`/`Bazaar`/`WebSessions`/`BCStates` 全 lazy-create
- `assistant@assistant` 登录：key-only（PasswordAuthentication no）

## 8. 最近一批提交（git log --oneline -8）

```
4629dbc docs: PROGRESS 381pass 前端落地
209b750 §45 真浏览器前端: HTTP + WebSocket 终端（第三个门）
9cfebb3 feat: 缺口2 GameBytecode brun SPAWN后台 fuel
60977f4 feat: 缺口5c MariaDB文件表 SQL子集 shell引号双修
f0fb22b feat: 缺口5b BGP会话 RIB撤回 traceroute-AS
45dc29f feat: 缺口5a 企业VLAN分段 网关allow 包路径双钩
79f0692 feat: P2剩余 多人公民 组织金库 悬赏trophy 异步PvP实证
3e0da75 feat: 复杂任务框架 Phase2 分阶段验证 前置gate 分期付款
```

## 9. 给接手 AI 的一句话

从 §4 的规约读起，动对应子系统前把 AGENTS.md 对应 gotcha 读完；
所有时间用 tick；所有新命令过 `TestNoDecorativeCommands` 的思想；
不给真执行；做完跑 `go test ./...` 全绿再提交。推送用 SSH key
`../lk`（remote 已设为 SSH + `core.sshCommand`）。
