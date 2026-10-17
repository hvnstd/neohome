# NeoHome — 进度状态（2026-10-17，分支 `arena/01a10c9c-neohome`）

`PROJECT.md` 是规格唯一权威来源；本文件只回答“每节做到哪了”，依据是代码、
测试和 `WORKSTREAM_NOTES.md`。状态只有三种：**已完成**（有实现+测试+验证）、
**部分**（能用但有明确缺口，缺口写出来）、**未开始**。

验证基线：`go build` ✓、`go vet ./...` ✓、`go test ./...` 433 pass 0 fail、
`gofmt -l` 空。Live 端口 `:2024`/`:2222`，存档默认 `world.gob`。

## 阶段

| 阶段 | 状态 | 说明 |
|---|---|---|
| MVP / Phase 0（§47） | 已完成 | 单玩家+Assistant+PC/路由/NAS+DNS 故障闭环：登录→排障→修网→赚钱→Assistant 自主赚钱 |
| Phase 1（§48） | 已完成 | 25 项全部有实现和测试；最后一批缺口（VM 快照、手机电源、cron 认电）已补 |
| Phase 2（§49） | 已完成 | 企业网/ISP/DC/Provider/Abuse/Trace/Evidence/执法（§34/§35）+ 黑市 + 复杂任务 + 多人/组织/异步 PvP |
| Phase 3（§50） | 已完成 | 社区包/任务/Mirror+Server+Backup Builder/资源调度/多实例/拓扑图/webd+browse 全部落地；真浏览器 WS/PTY 前端已落地（`internal/webtty`） |

## 分节状态

| # | 节 | 状态 | 依据 / 缺口 |
|---|---|---|---|
| 一 | 核心体验 | 已完成 | 四循环全可玩（观察→验证、IT 排障、黑客链、防守反制）；反制闭环到“反制/封锁/报告”——`assist defend` 让安全-track 助手每周期执行一步真实反制（CleanFoothold/口令轮换/Ban/§34 报案），花真实预算、全程留证 |
| 二 | 设计原则 | 已完成 | `TestNoDecorativeCommands` 机械锁死“无装饰命令”；WS-1.5 耦合补丁 |
| 三 | 世界模型 | 已完成 | `Device` 统一抽象，PC/路由/NAS/手机/VPS/VM/IoT 皆 Profile（`world.go`） |
| 四 | Linux 沉浸感 | 已完成 | 身份动词齐（useradd/userdel/usermod/groupadd/groups/chpasswd/passwd/su/sudo），四文件一致 |
| 五 | BusyBox | 已完成 | 38-applet 门控（`shell.go:394` + `APPLETS.txt`），ash 设备隔离 |
| 六 | fastfetch | 已完成 | 全读真实状态，区分设备 |
| 七 | 虚拟软件/架构 | 已完成 | Virtual Package/Binary，安装落文件/用户/服务/进程/端口 |
| 八 | 程序执行三类 | 已完成 | brun+SPAWN+fuel+文件/socket能力；`.py` 前端已落地（`python3` 把 Python 子集编译成同一套字节码，走同一 VM） |
| 九 | 软件包系统 | 已完成 | 名/版/架构/依赖/文件/服务/签名全建模 |
| 十 | APT/仓库/镜像 | 已完成 | 六发行版+签名+SYNCED/BEHIND/OFFLINE/PARTIAL/CORRUPTED+自建镜像 |
| 十一 | 供应链安全 | 已完成 | 签名校验、密钥过期、第三方恶意包（不知情 `postinst` 起后台服务并留日志） |
| 十二 | VPS | 已完成 | 区域/规格/v4/v6/开关/重装/调盘/快照/console/rDNS/SSH（WS-1.9） |
| 十三 | IP 系统 | 已完成 | v4/v6/私网/CGNAT/ULA/link-local/动态/共享/虚拟IP+归因不指人（WS-1.8） |
| 十四 | 家庭网络 | 已完成 | UCI 两段提交、iptables、NAT/过滤/UPnP/DMZ（WS-1.7） |
| 十五 | 家庭设备 | 已完成 | PC/笔记本/手机/路由/NAS/摄像头/门锁/打印机/交换机(PoE)/UPS/Server/VM Host（WS-1.10）；离线存储见 §41 |
| 十六 | VM | 已完成 | 状态机+资源后果+快照/回滚+存档往返 |
| 十七 | 资源管理 | 已完成 | CPU 分享/RAM→swap→OOM/满盘/磨损（SMART+fsck）/有限进程表（WS-1.11+后续） |
| 十八 | screen/tmux | 已完成 | 真进程+detach 重连 |
| 十九 | 通信系统 | 已完成 | 13 项全通+黑市+BBS私信+sftp -r+IMAPS；git 分支见§29 |
| 二十 | NPC 世界 | 已完成 | 记忆环+有向 standing+七处钩子+三处读回（聊天/collector/`people`） |
| 二十一 | NPC AI 架构 | 已完成 | 世界模拟 ✓ + agent 巡逻/日程/加固（规则层）+ §21 决策队列（`DecisionTick` 把规则层判不了的节点入队，MCP/`decisions` 可查可决，无 LLM 世界照跑） |
| 二十二 | Assistant | 已完成 | 忠诚+能力受限（CPU/权限/凭据/知识/网络/时间）+授权执行 |
| 二十三 | Assistant 成长 | 已完成 | skills 计数+train 方向+状态文本体现 |
| 二十四 | Assistant 资源 | 已完成 | Quota 进调度/swap/OOM/`free`/htop，`assist quota` 限主人，超硬件拒绝 |
| 二十五 | Assistant 钱包 | 已完成 | 家庭共享+子账户+任务预算 |
| 二十六 | 工作系统 | 已完成 | 白任务+分段任务+黑市交易；纯黑任务以后扩展（当前灰线由黑市承载） |
| 二十七 | 黑客玩法 | 已完成 | Recon→撤离全链路动词齐，同一套状态机 |
| 二十八 | 攻击面 | 已完成 | 全来自真实配置（转发/DMZ/UPnP/弱口令/暴露 IoT/防火墙） |
| 二十九 | 漏洞利用 | 已完成 | 框架完整（条件/后果/检测度/证据），8 个虚拟漏洞（telnet 默认口令/SMB 访客共享/guest 弱口令/cupsd 无鉴权/集市匿名投递箱等），共享端口→服务解析 |
| 三十 | 权限提升 | 已完成 | 公钥双向文件匹配+ssh-keygen+分发链；suid 经查不适用（无用户态 exec 语义，chmod 04000 落不到 FileMode） |
| 三十一 | 横向移动 | 已完成 | Server(10)/Workstation(20) 两段+网关 allow+默认拒+反向禁行；Admin/Backup/Guest 无设备可装（空 VLAN 是装饰） |
| 三十二 | Heat/Trace/Evidence | 已完成 | 证据图+权重+衰减+溯源（到运营商止） |
| 三十三 | 防守软件 | 已完成 | 防火墙/IDS/HIDS/审计/杀毒/fail2ban/完整性/集中日志/备份/监控（WS-1.12） |
| 三十四 | 取证/网管/ISP | 已完成 | 注册/NOC/Abuse 台/DC/企业网管+案件流（WS-1.13） |
| 三十五 | 执法 | 已完成 | 报案→网管→证据→案件→升级+资源账本+盲区（WS-1.13） |
| 三十六 | 因果关系 | 已完成 | 入侵链（reachable∧弱口令∧无监控）+磨损链（老盘∧负载∧时长）+欠费断网+OOM+DNS |
| 三十七 | 时间系统 | 已完成 | tick 驱动（30 sim-s），无等待墙是硬约束+测试锁死 |
| 三十八 | 地理设施 | 已完成 | BGP 会话/RIB/撤回黑洞+traceroute AS号；Cloud 由 VPS+镜像 infra 承载 |
| 三十九 | 互相耦合 | 已完成 | 措施+原则（改一环动全身，回归测试锁） |
| 四十 | 玩家之家 | 已完成 | 全套家当+第二公民；`player invite --own-home` 让公民拥有自己的家庭子网（独立 /24 + 路由 + NAS） |
| 四十一 | 地下室离线 | 已完成 | 离线 server+大盘+摆渡 routine；软件靠摆渡带（无包管理，注明） |
| 四十二 | 物理层 | 已完成 | 门/锁/摄像头/机柜/交换机/电源/UPS+数字绑定（断线/复位/物理访问） |
| 四十三 | PvP | 已完成 | 规格只要异步：双公民实证（入侵→取证→溯源→反杀，tick 间隔即 async） |
| 四十四 | 内容生产 | 已完成 | 编辑器=`job new`、社区格式=mission 文本、世界生成器=`worldgen household|enterprise|city`（住宅 8 lot、企业 4 站点、城市批量）；Package/Mission/Household 模板都在代码里 |
| 四十五 | 技术架构 | 已完成 | Go ✓ SSH/telnet ✓ 状态机 ✓ **真浏览器前端 ✓**（`internal/webtty`：手写 RFC 6455 + 终端引擎 + 第三入口 :8080；`browse`/`webd` 仍是世界内的网站服务，两者不同层）；数据是 gob 存档而非 SQLite（已记录为不适用，勿重开） |
| 四十六 | 数据模型 | 已完成 | Household 一等实体（成员+共用 infra+`Subnet`），支持多家庭子网；其余 36 项早齐 |
| 四十七 | MVP | 已完成 | 见阶段表 |
| 四十八 | Phase 1 | 已完成 | 见阶段表 |
| 四十九 | Phase 2 | 已完成 | 见阶段表 |
| 五十 | Phase 3 | 已完成 | 社区包/任务/Mirror+Server+Backup Builder/调度/多实例/拓扑图/webd 全落地，浏览器前端也已落地（`NEOHOME_WEB_ADDR`，活体验证见 `tools/_live_web_proof.mjs`） |
| 五十一 | 不做事项 | 遵守中 | 无真执行/无容器/无等待墙/无随机灾害/无点击 exploit（vet+测试+评审共同保证） |
| 五十二 | 最终目标 | 已完成 | 数字生活+攻防大循环全闭环；数据库运维（主从复制）、网站运营深度（真实流量+访问日志+`webstats`）、Assistant 训练深度（tracks 解锁真实能力+自动反制）均已补齐 |
| 附 | 体验补充 | 已完成 | 登录欢迎横幅（设备/账号/世界时间/入口，`showIntro` 只在玩家 PC 首提示触发，`motd` 随时重看）；`help <命令>` 按注册表给用途/用法/权限，未知命令报 `command not found`、未装 §33 工具报 `uninstalled`；`assist guide` DNS 链保留 + 修完后给状态化下一步（未验证任务/`assist train`/`scan`/`recon`）；低权限报错可诊断齐。可选可跳过、不阻塞 |

## 缺口清单（2026-10-17 更新）——全部关闭

原 8 批缺口、浏览器前端与下列 6 项均已关闭（记录见
`WORKSTREAM_NOTES.md` 的 P4b 段；对应测试锁死）：

1. ~~**真浏览器前端（§45 原文“Web 后续”）**~~ **已完成**——第三入口
   （HTTP+WebSocket 终端）在 `internal/webtty`，与 telnet/ssh 共用
   `core.LandPlayer`/`BeginSession`，会话进 `who`/syslog/证据；`resize`
   经新内建 `stty`/`tty` 可读。
2. ~~**数据库高级语义**~~ **已完成**——`GRANT`/`REVOKE`+多用户权限、
   `JOIN`、事务、**主从复制**（binlog 位点 + tick 应用 + `SHOW MASTER/
   SLAVE STATUS`、`CHANGE MASTER`、`START/STOP/RESET SLAVE`）。
3. ~~**真 3-way git merge**~~ **已完成**——merge commit 双 parent + 递归
   三路合并 + 冲突拒绝（`internal/core/git.go`）。
4. ~~**`.py` 前端**~~ **已完成**——`python3` 把整数变量/算术/`if`/`while`/
   `print` 子集编译成同一套 `[]BCInstr`，走 `BCRun`；未装即
   `command not found`（`internal/core/pycompile.go` + `internal/shell/py_cmds.go`）。
5. ~~**§21 的 LLM 唤醒调度**~~ **已完成**——`DecisionTick` 把规则层判不了
   的节点入队（入侵未被察觉/助手卡住/滥用到期/异常封禁），MCP
   `list_decisions`/`resolve_decision` 与 `decisions` 内建可查可决；无 LLM
   时世界照跑（`internal/core/decision.go`）。
6. ~~**§29 更多虚拟漏洞**~~ **已完成**——3 → 8 个，共享端口→服务解析，
   新后果均经真实协议在线上生效（`internal/core/vuln.go`）。
7. ~~**随机内容生长**~~ **已完成**——`worldgen household`（住宅 cap 8）、
   `worldgen enterprise`（4 站点双 VLAN）、`worldgen city`（批量）；
   所有网段由 `addr.go` 独占分配（`internal/core/worldgen.go`）。
8. ~~**多家庭子网**~~ **已完成**——`Household.Subnet` + per-`/24`
   分配/校验，`player invite --own-home` 让公民拥有独立家庭子网，默认
   世界逐字节不变（`internal/core/addr.go` / `players.go`）。

明确记录的两个不适用项（成本>收益，勿重开）：suid 建模、SQLite 后端
（gob 存档已验证）。
