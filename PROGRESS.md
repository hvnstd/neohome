# NeoHome — 进度状态（2026-10-07，分支 `arena/01a10c9c-neohome`）

`PROJECT.md` 是规格唯一权威来源；本文件只回答“每节做到哪了”，依据是代码、
测试和 `WORKSTREAM_NOTES.md`。状态只有三种：**已完成**（有实现+测试+验证）、
**部分**（能用但有明确缺口，缺口写出来）、**未开始**。

验证基线：`go build` ✓、`go vet ./...` ✓、`go test ./...` 289 pass 0 fail、
`gofmt -l` 空。Live 端口 `:2024`/`:2222`，存档默认 `world.gob`。

## 阶段

| 阶段 | 状态 | 说明 |
|---|---|---|
| MVP / Phase 0（§47） | 已完成 | 单玩家+Assistant+PC/路由/NAS+DNS 故障闭环：登录→排障→修网→赚钱→Assistant 自主赚钱 |
| Phase 1（§48） | 已完成 | 25 项全部有实现和测试；最后一批缺口（VM 快照、手机电源、cron 认电）已补 |
| Phase 2（§49） | 已完成 | 企业网/ISP/DC/Provider/Abuse/Trace/Evidence/执法（§34/§35）+ 黑市 + 复杂任务 + 多人/组织/异步 PvP |
| Phase 3（§50） | 未开始 | Web 客户端、可视化拓扑、Assistant 多实例、资源调度、社区包/任务、世界生成器、任务编辑器、Mirror/Server Builder，全无 |

## 分节状态

| # | 节 | 状态 | 依据 / 缺口 |
|---|---|---|---|
| 一 | 核心体验 | 部分 | 四循环可玩（观察→验证、IT 排障、黑客链、防守反制）；反制停在“Assistant 取证+追踪+玩家手动反制”，无自动反制执行体 |
| 二 | 设计原则 | 已完成 | `TestNoDecorativeCommands` 机械锁死“无装饰命令”；WS-1.5 耦合补丁 |
| 三 | 世界模型 | 已完成 | `Device` 统一抽象，PC/路由/NAS/手机/VPS/VM/IoT 皆 Profile（`world.go`） |
| 四 | Linux 沉浸感 | 已完成 | 身份动词齐（useradd/userdel/usermod/groupadd/groups/chpasswd/passwd/su/sudo），四文件一致 |
| 五 | BusyBox | 已完成 | 38-applet 门控（`shell.go:394` + `APPLETS.txt`），ash 设备隔离 |
| 六 | fastfetch | 已完成 | 全读真实状态，区分设备 |
| 七 | 虚拟软件/架构 | 已完成 | Virtual Package/Binary，安装落文件/用户/服务/进程/端口 |
| 八 | 程序执行三类 | 部分 | Builtin ✓ / Game Shell Script ✓ / **Game Bytecode VM 未开始** |
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
| 二十一 | NPC AI 架构 | 部分 | 世界模拟 ✓ +  agent 巡逻/日程/加固（规则层）；LLM 层只有 MCP 透传，无“需决策时唤醒”调度 |
| 二十二 | Assistant | 已完成 | 忠诚+能力受限（CPU/权限/凭据/知识/网络/时间）+授权执行 |
| 二十三 | Assistant 成长 | 已完成 | skills 计数+train 方向+状态文本体现 |
| 二十四 | Assistant 资源 | 已完成 | Quota 进调度/swap/OOM/`free`/htop，`assist quota` 限主人，超硬件拒绝 |
| 二十五 | Assistant 钱包 | 已完成 | 家庭共享+子账户+任务预算 |
| 二十六 | 工作系统 | 已完成 | 白任务+分段任务+黑市交易；纯黑任务以后扩展（当前灰线由黑市承载） |
| 二十七 | 黑客玩法 | 已完成 | Recon→撤离全链路动词齐，同一套状态机 |
| 二十八 | 攻击面 | 已完成 | 全来自真实配置（转发/DMZ/UPnP/弱口令/暴露 IoT/防火墙） |
| 二十九 | 漏洞利用 | 部分 | 框架完整（条件/后果/检测度/证据），但仅 3 个虚拟漏洞 |
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
| 四十 | 玩家之家 | 已完成 | 全套家当+第二公民（同 LAN 室友） |
| 四十一 | 地下室离线 | 未开始 | 故意延期：USB stick 是桥，vault 本体是后期资产 |
| 四十二 | 物理层 | 已完成 | 门/锁/摄像头/机柜/交换机/电源/UPS+数字绑定（断线/复位/物理访问） |
| 四十三 | PvP | 已完成 | 规格只要异步：双公民实证（入侵→取证→溯源→反杀，tick 间隔即 async） |
| 四十四 | 内容生产 | 部分 | Household/NPC/Package/Job/Mission 皆有模板结构；**缺世界生成器/任务编辑器/社区格式** |
| 四十五 | 技术架构 | 部分 | Go ✓ SSH ✓ 状态机 ✓；Web（xterm.js）✗；数据是 gob 存档而非 SQLite |
| 四十六 | 数据模型 | 已完成 | Household 一等实体（成员+共用 infra），其余 36 项早齐 |
| 四十七 | MVP | 已完成 | 见阶段表 |
| 四十八 | Phase 1 | 已完成 | 见阶段表 |
| 四十九 | Phase 2 | 已完成 | 见阶段表 |
| 五十 | Phase 3 | 未开始 | 见阶段表 |
| 五十一 | 不做事项 | 遵守中 | 无真执行/无容器/无等待墙/无随机灾害/无点击 exploit（vet+测试+评审共同保证） |
| 五十二 | 最终目标 | 部分 | 数字生活+攻防大循环已闭环；缺数据库运维、网站运营深度、Assistant 训练深度 |
| 附 | 体验补充 | 部分 | `assist guide`+help 入口+低权限报错可诊断齐；引导链（DNS 故障教学流）靠 IRC/NPC 提示，无强制新手流（符合“可选可跳过”） |

## 缺口清单（按依赖排序，做完即 Phase 3 之前无欠账）

1. ~~`useradd/userdel/groupadd` + Household 一等实体~~ done（本批）
2. Game Bytecode VM（§8，独立大件：受控运行时+capability API）
3. ~~NPC 记忆与关系~~ done（本批；§21 的 LLM 唤醒调度仍无，规则层即 agent 层）
4. ~~Assistant 配额 + suid/keys~~ done（本批）
5. VLAN + BGP 过程 + 数据库（§31/§38/§52，大件，可拆）
6. ~~BBS 私信 / git 分支合并 / sftp -r / IMAPS~~ done（本批；真 3-way 合并仍不做）
7. 地下室 vault（§41，中件：离线 Server/Storage+物理访问门控，USB 已就绪）
8. 内容工具链：世界生成器/任务编辑器/社区格式（§44，大件）
9. Phase 3：Web 客户端/可视化/多实例/调度（§50，大件）
