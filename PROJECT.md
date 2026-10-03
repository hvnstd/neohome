# NeoHome — 项目规格与计划（用户原文，2026-10-02）

> 本文件是项目定位的唯一权威来源（verbatim）。实现优先级的依据都在这里。


你现在开始负责实现这个项目。

不要把它理解成一个普通“黑客游戏”，也不要把它做成“Linux 教学软件套黑客皮肤”。项目的真正定位是：

**一个持续运行、尽可能自洽、以计算机 / Linux / 家庭网络 / VPS / 互联网基础设施为底层世界的模拟游戏。**

玩家平时可以正常使用电脑、管理服务器、安装软件、配置网络、运行服务、赚钱、建设自己的数字家庭；当玩家主动进入黑客玩法后，同一套真实世界状态继续被用于侦察、渗透、权限提升、横向移动、防守、取证、反制和追踪。

核心原则：

> 文件可以是模拟的，二进制可以是模拟的，真实机器码不能直接执行；但是程序对游戏世界造成的后果必须是真的。

也就是说：

* 不执行玩家提供的真实宿主机代码
* 不运行真实 ELF / PE / 恶意二进制
* 不给每个玩家创建真实 Docker / VM / Linux 容器
* 通过 Go 内部状态机、虚拟文件系统、虚拟网络、虚拟进程、虚拟服务和受控程序运行时模拟整个世界
* 玩家看到的行为尽可能接近真实 Linux / Unix
* 配置改变后必须真实影响其他系统
* 不使用无意义的等待墙
* 不为了“真实”而加入没有实际作用的功能

---

# 一、游戏的核心体验

核心循环：

观察
→ 建立假设
→ 动手验证
→ 世界状态真的变化
→ 得到新的线索
→ 继续处理

正常 IT 循环：

使用设备
→ 安装软件
→ 配置系统
→ 运行服务
→ 监控资源
→ 发现问题
→ 查看日志
→ 排障
→ 修复
→ 获得收入
→ 建设更复杂基础设施

黑客循环：

信息搜集
→ 识别目标
→ 分析攻击面
→ 获得初始访问
→ 权限提升
→ 内部侦察
→ 横向移动
→ 持久化
→ 达成目标
→ 降低暴露
→ 撤离

防守循环：

异常
→ 日志
→ 监控
→ 分析
→ 定位
→ 隔离
→ 修复
→ 加固
→ 取证
→ 追踪

反制循环：

发现入侵
→ Assistant / 网管调查
→ 收集证据
→ 定位来源
→ 分析中间节点
→ 对攻击者进行反制 / 封锁 / 报告

---

# 二、核心设计原则：服务必须真的有用

任何加入游戏的服务、命令、软件、设备，都必须问：

“它会不会改变世界状态？”

如果不会，就不要实现。

例如：

DHCP：

* 真正分配 IP
* 真正分配网关
* 真正分配 DNS

DNS：

* 真解析域名
* 配置错误会导致域名访问失败
* IP 直连仍可能正常

NAT：

* 真决定内网到公网的连接关系

Firewall：

* 真决定连接能不能建立

SSH：

* 真提供远程 Shell

Telnet：

* 可用于老设备、特殊网络和游戏世界中的旧系统

HTTP / HTTPS：

* 真提供网页和 API
* 服务状态会影响访问

SMB / NFS：

* 真共享文件

FTP / SFTP：

* 真传文件

SMTP / IMAP：

* 真处理邮件

IRC / BBS / Chat：

* 真提供 NPC / 玩家通信
* 成为任务、情报和社会系统的一部分

Cron：

* 真定时执行任务

systemd / init：

* 真控制服务生命周期

ps / top / htop：

* 真反映当前虚拟进程状态

kill：

* 真终止虚拟进程

nice：

* 真影响虚拟 CPU 调度

screen / tmux：

* 真维持后台会话
* 玩家断开 SSH 后，后台工作仍继续

日志：

* 真记录事件
* 可以用于排障、调查和取证

---

# 三、底层世界模型

所有设备都尽量使用统一抽象：

Device
├── Hardware
│   ├── CPU
│   ├── RAM
│   ├── Disk
│   └── Network Interfaces
├── OS
├── Virtual Filesystem
├── Users
├── Groups
├── Permissions
├── Processes
├── Services
├── Network Config
├── Routing
├── Credentials
├── Logs
└── State

PC、Router、NAS、Phone、VPS、VM、IoT 都只是不同默认 Profile。

例如：

Router Profile：

* Embedded Linux
* BusyBox
* DHCP
* DNS Forwarder
* NAT
* Firewall
* Wi-Fi
* 少量存储
* 低 RAM
* 简单 CPU

VPS Profile：

* Debian / Ubuntu / Alpine / Arch / Fedora 等
* 完整 Shell
* SSH
* systemd 或对应 init
* APT/APK/Pacman/DNF 等
* 多服务
* 可安装更多软件
* 可分配资源

---

# 四、Linux / Unix 模拟必须有沉浸感

必须支持大量真正有意义的常见命令。

基础：

ls
cd
pwd
cat
cp
mv
rm
mkdir
touch
echo
printf

文件与搜索：

find
grep
head
tail
sort
uniq
wc
du
df
mount
lsblk

系统：

uname
hostname
uptime
whoami
id
env
free
lscpu
dmesg

进程：

ps
top
htop
kill
pkill
nice

网络：

ip
ifconfig
route
ss
ping
traceroute
dig
nslookup
curl
wget

权限：

chmod
chown
sudo

服务：

systemctl
service
init.d
对应发行版机制

远程：

ssh
scp
sftp

终端：

screen
tmux

包管理：

apt
apk
pacman
dnf

其它常见工具按实际作用逐渐增加。

注意：
不要为了“命令数量”堆命令。
每个实现的命令必须真正读写游戏世界状态。

---

# 五、BusyBox

BusyBox 可以直接作为思路和参考，甚至可以研究其公开代码结构，但不能直接执行其宿主机程序。

路由器 / IoT 等设备可以提供 BusyBox 风格环境：

ash
cat
cp
mv
rm
ps
top
kill
ip
route
ifconfig
udhcpc
nslookup
wget
httpd
crond
syslogd
logread
dmesg

不同设备只暴露实际支持的命令。

---

# 六、fastfetch 等“系统信息工具”

fastfetch 必须加入。

它不是装饰，而是读取真实虚拟世界状态：

fastfetch
→ OS
→ Host
→ Kernel
→ CPU
→ RAM
→ Disk
→ Packages
→ Processes
→ Uptime
→ Shell
→ Terminal
→ IP
→ IPv6

不同设备看到不同结果。

例如：

VPS：
Debian 13
4 vCPU
8 GiB RAM
80 GB Disk

Router：
Embedded Linux
BusyBox
128 MiB RAM
MIPS / ARM 等

Assistant Node：
Assistant 自己的系统环境、资源、进程和 Uptime

同类工具也可以加入：

uname
hostname
whoami
id
uptime
free
lscpu
lsblk
df
ps
htop

---

# 七、虚拟软件和二进制架构

绝对不能把真实 ELF/PE 二进制塞给玩家然后执行。

采用 Virtual Package / Virtual Binary 模型。

例如玩家：

apt install nginx

游戏实际上：

下载虚拟包
→ 检查签名
→ 解包虚拟文件
→ 写入虚拟 FS
→ 创建用户
→ 创建配置
→ 注册服务
→ 注册虚拟 binary
→ 启动虚拟进程

/usr/sbin/nginx 在虚拟文件系统中确实存在，但不是宿主机上可执行的真实机器码。

它可以具备：

权限
大小
hash
mtime
版本
路径
依赖

但最终运行：

virtual binary
→ builtin implementation
→ Game Runtime
→ World State

---

# 八、程序执行分三类

## 1. Builtin Program

核心系统软件使用 Go 实现。

例如：

htop
ssh
sshd
nginx
dns
dhcp
cron
screen
tmux
systemctl
apt

它们直接操作模拟世界。

## 2. Game Shell Script

提供游戏自己的 shell。

脚本可以：

读取虚拟文件
写文件
调用虚拟命令
启动虚拟程序
连接虚拟网络
读环境变量
操作进程

但不能直接访问宿主机。

## 3. Game Bytecode / VM

以后允许玩家或社区写程序。

程序编译成游戏专用 Bytecode。

允许类似：

LOAD
STORE
CALL
JMP
READ_FILE
WRITE_FILE
OPEN_SOCKET
READ_SOCKET
WRITE_SOCKET
SPAWN_PROCESS
EXIT

所有能力通过 capability API 提供。

不能：

* syscall 宿主机
* 读宿主机文件
* 创建宿主机网络连接
* 执行宿主机机器码

恶意程序最多把“游戏里的 CPU”吃满。

---

# 九、软件包系统

必须做成真正的软件生态。

Package：

* 名称
* 版本
* 架构
* 依赖
* 文件
* 权限
* 服务定义
* 配置模板
* 安装脚本
* 卸载脚本
* 签名
* Repository
* Release

尽量模拟 Debian / Alpine / Arch / Fedora 等真实生态的概念。

软件安装不能只有：

Installed successfully

而应该真正产生：

文件
用户
目录
配置
服务
端口
进程
日志
依赖

---

# 十、APT / Repository / Mirror

必须存在不同发行版和不同来源：

Debian
Ubuntu
Alpine
Arch
Fedora
OpenWrt
以及后续可以继续扩展的发行版。

不同发行版：

* 不同包管理器
* 不同仓库
* 不同文件布局
* 不同服务管理
* 不同默认配置
* 不同软件版本生态

Repository 是世界里的真实网络节点。

例如：

Official Repo
Regional Mirror
Community Mirror
Company Repo
Private Repo
Old Archive

镜像服务器拥有自己的：

* IP
* DNS
* ASN / Provider
* Region
* Latency
* Sync Status
* Supported Releases
* Supported Architectures

状态可以有：

SYNCED
BEHIND
OFFLINE
PARTIAL
CORRUPTED

允许出现：

官方源
→ 一级镜像
→ 地区镜像
→ 公司内部镜像
→ 本地缓存

例如玩家可以自己搭：

apt-cacher-like
Repository
Mirror
Archive
Package Cache

“镜像服务器”本身应该是真正可访问的服务器。

---

# 十一、APT 安全 / 软件供应链

不要随机“APT 被病毒感染”。

正常情况下：

Official Repo
→ Signed Metadata
→ Package Verification
→ Install

加入第三方源后，风险才改变。

可以存在：

官方源
第三方源
未知源
签名错误
密钥过期
包版本异常
恶意第三方包
配置错误

但游戏里的恶意代码依然只能是虚拟程序。

可以实现类似：

Package
├── Metadata
├── Virtual Files
├── Dependencies
├── Service Definition
├── Game Install DSL

不要执行任意宿主机 shell。

---

# 十二、VPS / 计算机 / 组网

VPS 必须是世界中的真实节点。

玩家可以：

购买 VPS
选择区域
选择规格
选择 IPv4 / IPv6
选择 OS
分配磁盘
启动 / 关机
重启
重装系统
恢复快照
添加磁盘
调整资源
进入 Console
SSH

VPS 拥有：

CPU
RAM
Disk
Public IPv4
IPv6
Provider
Datacenter
ASN
rDNS
OS
Virtual Network Interface

服务器获得公网 IP 后，它就进入整个虚拟互联网。

---

# 十三、IP 系统

支持：

公网 IPv4
公网 IPv6
RFC1918 私网
CGNAT / Shared Address Space
Loopback
Link-local
IPv6 ULA
IPv6 link-local
Dynamic IP
Shared IP
Virtual IP
NAT Mapping

重要：

“公开 IP”不等于“公开玩家真实身份”。

查询 IP 时首先可能得到：

Provider
ASN
Region
Network Range
Abuse Contact
rDNS

而不是玩家现实个人信息。

---

# 十四、家庭网络必须符合现实习惯

普通家庭：

Internet
→ ISP
→ NAT Router
→ LAN

默认：

WAN → LAN：阻断
LAN → WAN：允许

家用 Router 默认一般不会主动向公网暴露管理服务。

攻击面来自玩家自己配置出来的问题：

Port Forward
DMZ
UPnP
Remote Management
Old Firmware
Weak Credentials
Exposed IoT
Misconfigured Firewall

不要做：

“系统今天安排你家路由器开放端口。”

必须是配置和因果关系产生结果。

---

# 十五、家庭设备

至少支持：

PC
Laptop
Phone
Router
NAS
Camera
Smart Lock
IoT
Printer
Switch
UPS
Server
VM Host

设备之间必须建立依赖关系。

例如：

Camera
→ PoE Switch
→ Router
→ NAS

Router DNS 出错：

域名失败
IP 直连可能仍然成功

Switch 关闭：

连接它的设备掉线

NAS 停止：

共享 / 备份 / 相册功能失败

UPS 异常：

停电风险提高

---

# 十六、VM

VM 可以加入游戏，而且应该有意义。

例如：

Host
├── VM01
├── VM02
├── VM03
└── VM04

VM 有：

CPU
RAM
Disk
Network
OS
Services

但是不要给每个玩家真的启动宿主机 VM。

VM 是虚拟状态机。

资源要真正产生影响：

RAM 不足
→ Swap
→ 性能下降

CPU 不足
→ Processes 变慢

Disk 满
→ 服务异常 / 写入失败

---

# 十七、系统状态 / 资源管理

资源不是装饰。

CPU
RAM
Swap
Disk
Disk I/O
Network bandwidth
Process Count

必须真实影响系统。

例如：

htop
显示当前真实游戏状态。

进程 CPU 占用高：
系统其他任务变慢。

RAM 不够：
进程被杀 / swap / 服务异常。

磁盘满：
日志无法写入 / 数据库无法写入 / 安装失败。

---

# 十八、screen / tmux

必须真正存在。

玩家：

tmux new -s bot
运行程序
detach

退出 SSH 后进程继续。

重新连接：
tmux attach

screen 同理。

这使“后台运行服务”和真实 Linux 工作习惯产生关联。

---

# 十九、通信系统

世界里必须存在一个真正的数字社会。

至少：

DNS
WHOIS/RDAP-like Registry
HTTP
HTTPS
SMTP
IMAP
IRC
BBS
FTP
SFTP
Git
Job Board
Banking
Package Repository

IRC / BBS / Chat 不只是装饰。

它们可以承载：

NPC 通信
玩家通信
工作
交易
情报
漏洞线索
黑客组织
黑市
企业信息
社会关系

NPC 是“网络上的人”，而不是静态任务发布器。

---

# 二十、NPC / AI 世界

目前只有一个真人玩家，所以世界不能只依赖真人在线。

NPC 必须拥有自己的：

账号
钱包
设备
IP
联系人
工作
技能
服务
文件
权限
日程
记忆
行为

NPC 可以：

上班
回家
买设备
更新系统
维护服务器
发邮件
使用 IRC
接工作
发布需求
租 VPS
部署服务
升级网络
雇佣 Assistant
遭受攻击
防守
报警
寻求玩家帮助

---

# 二十一、NPC AI 架构

不要让所有 NPC 每秒都调用 LLM。

分三层：

World Simulation：
时间
网络
金钱
设备
进程
任务
库存
日志
状态

Agent Layer：
目标
计划
选择行为
处理事件

LLM Layer：
自然语言
聊天
复杂规划
推理
异常决策

只有在真正需要智能决策时唤醒 Agent / LLM。

世界必须能在没有 LLM 的时候继续正常运行。

---

# 二十二、Assistant

Assistant 是玩家的重要长期伙伴。

基本原则：

**忠诚、无条件协助玩家、不背叛玩家。**

但是：

无条件帮忙 ≠ 无限能力。

Assistant 受：

CPU
RAM
设备权限
软件
凭据
知识
网络
时间
资源

限制。

Assistant 可以：

检查服务器
查看日志
维护系统
接任务
赚钱
管理设备
备份
监控
防守
取证
分析异常
在玩家授权下执行反制

---

# 二十三、Assistant 成长

Assistant 能力随长期使用成长。

例如：

Linux
Networking
Programming
System Administration
Security
Incident Response
Social / Communication

不是简单：

“Level 12 / +10%”

而是实际积累经验与能力。

例如最开始：

“我发现 nginx 不工作。”

后期：

“nginx 进程正常，80 端口正常，DNS 解析正常，检查 upstream 后发现后端服务 OOM，建议恢复服务并限制 VM 内存。”

---

# 二十四、Assistant 的计算资源

玩家可以给 Assistant 分配资源。

例如：

Household：
CPU 8
RAM 16 GB

Player：
2 CPU / 6 GB

Assistant：
1 CPU / 2 GB

Server：
5 CPU / 8 GB

Assistant 甚至可以有自己的：

PC
VM
工作目录
SSH Key
脚本
日志
任务
缓存
配置

例如：

/home/assistant

玩家也可以 SSH 进去查看 Assistant 的工作环境。

---

# 二十五、Assistant 钱包

玩家与 Assistant 钱包属于同一家庭资产体系。

例如：

Household Wallet
├── Main Balance
└── Assistant Budget

Assistant 完成工作：
家庭财富增加。

Assistant 可以拥有：

* 自己的可支配余额
* 支出限制
* 任务预算
* 设备购买权限

玩家可以调整资金权限。

可以存在：

共享家庭资产
+
Assistant 子账户

而不是两个完全无关的钱包。

---

# 二十六、工作系统

不要让赚钱只靠黑客。

正常工作：

修电脑
修 DNS
修 Wi-Fi
修 Web
配置服务器
部署网站
修邮件
维护镜像
网络排障
系统维护
企业 IT
安全审计
应急响应

黑色 / 灰色任务以后再扩展。

所有任务必须落回真实世界状态。

例如：

“公司镜像三天没同步”

玩家调查：

cron
→ sync service
→ upstream
→ network
→ disk
→ logs

最终修复。

---

# 二十七、黑客玩法

黑客不是独立小游戏，而是同一套 OS / Network / Service 系统的另一种用法。

流程：

Recon
→ Target Identification
→ Attack Surface
→ Initial Access
→ Privilege Escalation
→ Internal Recon
→ Lateral Movement
→ Persistence
→ Objective
→ Evidence Reduction
→ Exit

Recon 来源：

DNS
WHOIS/RDAP
BGP / ASN
公开网站
邮件
BBS
IRC
公开文件
服务 Banner
账号信息
NPC 行为

---

# 二十八、攻击面

攻击面来自世界真实状态：

开放服务
错误配置
过时系统
弱认证
错误权限
第三方软件
不安全服务
供应链问题
暴露 IoT
远程管理
网络拓扑

不是系统硬塞一个“漏洞图标”。

---

# 二十九、漏洞与 Exploit 的处理

可以有大量虚拟漏洞和 Exploit。

但是：

不执行真实漏洞攻击代码
不把玩家输入当真实 payload 执行
不让游戏成为现实攻击环境

漏洞可以定义为：

Target Requirements
Success Conditions
Side Effects
Detection Level

例如：

某旧服务
+
特定版本
+
错误配置

可以产生：

临时 shell
低权限账号
配置泄露
信息泄露

等等。

所有后果由游戏世界状态机处理。

---

# 三十、权限提升

账号必须真正有：

UID
GID
Groups
File Permissions
sudo rules
Service Accounts
SSH Keys
Credential Access

玩家从：

www-data

可能通过合理的游戏世界关系：

app
→ backup
→ admin
→ root

但必须依靠调查和理解系统状态。

---

# 三十一、横向移动

企业网络必须有：

WAN
DMZ
LAN
Server VLAN
Admin VLAN
Backup Network
Guest Network

从公网进入 DMZ 后：

才可能看到内部网络。

内部设备：

Web
DB
NAS
Backup
Admin
Employee PC
Mail
Git
Monitoring

不同设备的可访问性由 Routing / Firewall / Permissions 决定。

---

# 三十二、Heat / Trace / Evidence

不要只用：

Heat = 73

而应该保存真正的证据链。

例如：

攻击事件
→ Source IP
→ VPS
→ Provider
→ SSH account
→ timestamp
→ Target Log
→ Network Log
→关联事件

UI 可以把它总结成 Heat / Trace，但底层应该是真实 Evidence Graph。

玩家的每个动作可能产生：

Network Event
Process Event
File Event
Authentication Event
IDS Alert
Log Entry

成功不等于没有留下证据。

---

# 三十三、防守和安全软件

加入真正有功能的安全工具：

Firewall
IDS
HIDS
Audit
Antivirus / EDR-like
Fail2ban-like
Integrity Monitor
Central Log Server
Backup
Monitoring

它们必须真的影响世界。

例如：

IDS：
发现扫描、爆破、异常流量

Fail2ban：
多次失败登录后自动封 IP

Integrity Monitor：
关键文件发生修改后报警

Central Logs：
从多个设备集中收集日志

---

# 三十四、取证 / 网管 / ISP / Provider

世界中必须有：

家庭网管
企业网管
ISP
Hosting Provider
Datacenter Admin
Security Team
Abuse Desk
Law Enforcement

它们不是万能 NPC。

它们依靠真实世界状态中的：

日志
流量
账号
时间
IP
Provider
证据

逐步调查。

---

# 三十五、FBI / Law Enforcement

可以存在 FBI 或抽象成 Law Enforcement。

但是不能：

今天做了坏事
→ FBI 明天瞬移到家

必须：

行为
→ 受害者
→ 报案
→ 网管 / ISP / Provider
→ 日志 / 证据
→ 案件
→ 调查
→ 更高等级调查

调查有自己的资源、权限和信息盲区。

“IP 查到谁”也不能一步到现实身份。

IP 首先更可能对应：

Provider
ASN
Datacenter
Network Range
Abuse Contact

进一步需要更多合法调查证据。

---

# 三十六、现实网络中的因果关系

世界里任何异常最好都能找到原因。

不要随机：

今天停电
明天被黑
后天硬盘炸掉

而应该是：

老硬盘
+
高负载
+
长期运行
→ 故障概率上升

公网开放 RDP
+
弱认证
+
无监控
→ 入侵风险上升

长期欠 ISP 费用
→ 断网

VM 内存分配太小
→ Swap
→ OOM
→ 服务退出

DNS 配置错误
→ 域名失败
→ IP 直连正常

每个异常尽量都能回溯出因果链。

---

# 三十七、时间系统

时间不是等待墙。

世界按时间运行：

NPC 日程
设备 uptime
服务运行时间
任务截止
日志时间
网络状态
商业活动
Assistant 工作

现实几秒到几分钟的事情可以合理压缩到游戏里的数秒。

核心禁止：
用 10 分钟 / 1 小时 / 8 小时这种纯人为等待阻挡玩法。

---

# 三十八、地理 / 网络基础设施

逐渐建设：

家庭网络
VPS
Datacenter
ISP
ASN
BGP
Transit
IXP
CDN
Mirror
Cloud

让互联网本身成为可以观察、调查和利用的地图。

---

# 三十九、服务器 / 软件 / 服务之间必须互相耦合

例如：

VPS
→ Debian
→ apt
→ install nginx
→ systemd service
→ process
→ socket
→ firewall
→ public IP
→ DNS
→ HTTP
→ browser / curl

玩家改动任何一环，其他环都可能受影响。

这就是项目最大的差异化。

---

# 四十、玩家之家

每个玩家都有属于自己的数字家庭。

Home
├── Router
├── PC
├── Phone
├── NAS
├── Assistant PC
├── Camera
├── Smart Lock
├── Server
├── VM Host
└── Offline Storage

家庭网络可以逐渐发展成：

单路由
→ 多设备
→ NAS
→ Server
→ VLAN
→ VM
→ Assistant Infrastructure
→ 高级防守

“家”本身就是玩家长期成长的资产。

---

# 四十一、地下室 / Air-gapped Storage

可以加入后期资产：

Offline Server
Offline Storage
Air-gapped Backup

优点：
远程攻击无法访问。

缺点：
必须物理访问。

它不能成为“绝对保险箱”，但可以成为真正离线灾备。

---

# 四十二、物理层

加入：

门
门锁
摄像头
机柜
交换机
电源
UPS
地下室
物理访问

物理层和数字层真正绑定。

例如：

拔 Router
→ 网络断

按设备 Reset
→ 配置恢复

进入机房
→ 获得物理访问

摄像头
→ 产生视频 / 证据状态

---

# 四十三、PvP

早期不需要实时双人互殴。

采用异步持久 PvP：

A 今天入侵 B
→ 修改 B 的世界状态
→ B 第二天上线
→ 调查异常
→ 找证据
→ 追踪
→ 反击 A

玩家攻击的是：

设备
配置
账号
服务
数据
网络

而不是一个实时动作游戏血条。

---

# 四十四、内容生产

必须从第一天设计模板系统。

家庭模板：

name
devices
network
users
services
credentials
routine
security
secrets
jobs

NPC 模板：

identity
skill
knowledge
routine
relationships
devices
accounts
wallet
goals

Package 模板：

name
version
architecture
dependencies
files
services
binary
scripts
signature

Mission 模板：

objective
target
prerequisites
expected_state
possible_solutions
evidence
reward

以后实现世界生成器、任务编辑器和社区投稿格式。

---

# 四十五、技术架构

首选：

Go

原因：
单二进制
高并发
长连接
SSH / PTY
内存模拟
状态机
服务器部署简单

入口：

SSH
使用 charmbracelet/wish 等方案。

Web 后续：
xterm.js
WebSocket

数据：

SQLite 开发
Postgres 后期

玩家状态：
设备状态
Filesystem
Process
Service
Network
Accounts
Wallet
NPC
Assistant

都应该是数据，不是真实 Linux 进程。

---

# 四十六、核心数据模型

至少需要：

World
Player
Household
Device
OS
Filesystem
File
Process
Service
Package
Repository
Mirror
NetworkInterface
IPAddress
Route
FirewallRule
User
Group
Credential
LogEntry
Event
Task
NPC
Assistant
Wallet
Provider
VPS
Datacenter
ASN
DNSRecord
Mail
ChatChannel
Evidence
Case

---

# 四十七、MVP / Phase 0

不要一开始做整个互联网。

先实现最小闭环：

1 个真人玩家
1 个 Assistant
1 台 PC
1 台 Router
1 个小 NAS
1 个 ISP
1 个简单 VPS Provider
1 个 Chat / IRC Server
1 个 Mail 系统
1 个 Bank
1 个工作市场
1 个 NPC 邻居

必须真正跑通：

登录
→ fastfetch
→ shell
→ Router
→ DHCP
→ DNS
→ Internet
→ SSH
→ htop
→ tmux / screen
→ apt
→ 安装软件
→ 启动服务
→ 产生端口
→ 访问服务
→ 修复故障
→ 完成工作
→ 赚钱
→ Assistant 帮忙
→ Assistant 自己工作
→ 钱包共享
→ 买资源
→ 开 VM
→ 部署新服务
→ 服务进入网络

然后开始：

Recon
→ 扫描
→ 找公开服务
→ 找虚拟漏洞
→ 获得初始访问
→ 权限
→ 日志
→ 防守

---

# 四十八、Phase 1

加入：

Phone
USB
SMB
NFS
NAS
Camera
Smart Lock
DNS
SMTP
IMAP
FTP
HTTP
HTTPS
Git
BBS
IRC
Job System
Assistant Growth
Security Tools
VPS
多发行版
Repository
Mirror
Snapshots
VM

---

# 四十九、Phase 2

加入：

多人
异步 PvP
企业网络
ISP
Datacenter
Provider
Abuse
Trace
Evidence
Law Enforcement
组织 / Clan
Black Market
更复杂任务

---

# 五十、Phase 3

加入：

Web Client
可视化网络拓扑
Assistant 多实例
资源调度
社区软件包
社区任务
世界生成器
任务编辑器
Mirror Builder
Server Builder

---

# 五十一、不要做的事情

不要：

* 每个玩家启动真实 Docker
* 每个玩家创建真实 VM
* 执行真实玩家二进制
* 执行真实恶意代码
* 用等待时间制造难度
* 做无意义的 500 个命令
* 做纯数字等级
* 随机安排灾害
* 随机安排入侵
* 做“点击 Exploit”
* 做“Black Box Hack Minigame”
* 把 Linux 命令做成装饰按钮

---

# 五十二、最终目标

最终玩家应该能：

正常生活：
使用 PC
管理 Linux
部署服务
维护家庭网络
维护 VPS
搭建服务器
运营网站
维护邮件
搭建 IRC
维护镜像
管理数据库
分配计算资源
训练 Assistant
赚钱

也可以：

Recon
渗透
权限提升
横向移动
隐藏
取证
防御
追踪
反制

而所有这些都发生在：

**同一个持续运行的世界、同一套设备状态、同一套网络、同一套文件系统、同一套进程模型、同一套服务模型、同一套经济体系。**

项目的最终核心理念：

> **玩家不是在“玩一个黑客模拟小游戏”。**
>
> **玩家拥有一套真正属于自己的数字生活基础设施。**
>
> **黑客行为只是玩家学会如何使用、理解、攻击和防守这个世界之后，自然而然产生的一种玩法。**

现在不要停在设计阶段。

直接开始实现。

要求：

1. 先建立可运行的 Go 项目骨架
2. 建立 World / Device / FS / Process / Network / Service 的基础模型
3. 实现 SSH Shell
4. 实现 PC + Router 两种设备
5. 实现基础 DHCP / DNS / NAT / Routing
6. 实现 BusyBox 风格 shell
7. 实现 fastfetch / ps / htop / ip / ss / ping / dig 等基础工具
8. 实现 screen / tmux 的最小真实模拟
9. 实现虚拟文件系统
10. 实现虚拟进程
11. 实现虚拟服务
12. 实现最小 Package / Repository / apt
13. 实现 nginx 一类的 builtin service
14. 实现简单 VPS
15. 实现 Assistant Agent
16. 实现 Bank / Job
17. 实现最小 Chat / IRC
18. 实现日志
19. 实现一个完整故障链：

Router 配置错误
→ PC DNS / 网络异常
→ 玩家调查
→ htop / ip / ss / dig / logs
→ 修复 Router
→ 网络恢复
→ 完成工作
→ 玩家赚钱
→ Assistant 获取工作并赚钱

完成后继续向下扩展，不要为了“完整设计”而停下来等待我的下一条命令。

每一步都优先保证：

**真实状态 > 漂亮 UI**
**系统耦合 > 功能数量**
**因果关系 > 随机事件**
**可玩性 > 等待**
**安全模拟 > 真代码执行**
**长期世界 > 任务脚本**

---

# 体验设计补充：可探索的网络与循序渐进的引导

本节是基于后续体验讨论新增的设计补充，不替换或删改以上项目规格。可参考 Telehack 的“大厅可发现命令、网络主机可探索、低权限账户先观察”的体验结构，但 NeoHome 必须保留自己的现代家庭网络题材、因果模拟与安全边界，不复刻其历史内容、主机资料或具体命令实现。

## 玩家进入世界时

玩家第一次连接后，应尽快知道自己身处什么环境、可以从哪里开始探索，而不是面对没有上下文的空白终端：

* 展示简短欢迎信息、当前设备和账号、模拟世界时间，以及 `help` / `?` 的入口。
* 新手说明可选、可跳过、可随时重看；不以长教程或现实等待时间阻塞玩家。
* 命令帮助必须来自实际注册并可用的命令。`help <命令>` 展示用途、参数和权限要求；未知或当前设备不支持的命令要明确报错。
* 登录后提供一个轻量的本地探索入口，例如从终端查看已知主机、服务或公开资料；列出的目标必须来自真实世界状态和可达性规则，不能是装饰清单。

## 探索与低权限观察

网络地图应通过玩家可执行的操作逐步展开。主机可以具有名称、角色/组织、网络位置、公开服务和可见资料；玩家查到什么取决于 DNS、路由、防火墙、服务状态、权限和已收集证据。不能仅凭“地图上有主机”就绕过访问控制。

* 公共目录、公告、服务 banner、IRC/BBS、DNS、注册信息和公开文件，作为逐步认识世界的入口。
* Guest 或低权限账号只允许读取该账号实际可访问的文件、服务和信息；不得提供免费 root 或跨越权限边界的万能观察能力。
* 权限不足、网络不可达、DNS 失败、服务停止等结果应使用一致且可诊断的错误，并尽可能指出可继续调查的方向，但不直接泄露机密或给出自动通关结果。
* 网络和主机视图随玩家调查、配置变更和世界事件更新；修复服务后可达性随之改变，服务或链路故障时也应如实反映。

## 开局 Assistant 引导

Assistant 是世界内的引导伙伴，不是外接 LLM 的替身，也不应该代替玩家解谜。基础引导由 Go 状态机和规则提供，关闭 MCP 或外部模型时仍可完整游玩。

* 建议必须基于 Assistant 当前真实可访问的状态、日志、文件、服务和权限；没有检查过的事实不能伪装成已诊断结论。
* 采用逐步线索：先指出可观察症状，再建议调查位置；玩家取得新证据后再给更具体的解释和下一步方向。
* 可以回答“我能帮什么”“下一步可检查什么”等问题，并给可选命令示例；避免未经请求直接修改玩家设备。
* 玩家授权 Assistant 执行修复时，操作必须通过同一套模拟命令/权限边界，产生实际世界变化、日志和可追溯事件。
* 对开局 DNS 故障，理想引导链是：症状（IP 通而域名失败）→ 建议检查解析结果与日志 → 找到路由器上游配置原因 → 玩家修复 → 域名访问、镜像拉取和受影响的定时任务恢复。

## 高效实施顺序与验收

本节只规定实现顺序，不缩减前文目标，也不要求重做已经存在的能力。开始每项工作前，先对照当前代码、测试和工作流记录确认现状；已有功能若缺少验收，就补验证或修复缺口，不复制实现。

按依赖推进，优先完成一个端到端可玩的纵向切片，再扩展内容：

1. **建立基线，不重搭地基。**运行现有测试、构建并核对相关状态模型和命令；列清这项切片已有、缺失、失效的环节。保持已有的 Go 状态模拟、虚拟文件/进程/网络边界和持久化方式，不引入真实宿主机执行环境。
2. **完成首次可玩闭环。**先让玩家知道当前设备、账号、可用命令和可探索入口；再打通“发现 DNS/网络症状 → 用命令与日志调查 → 修复真实配置 → 连通性恢复 → 完成工作并获得报酬 → Assistant 基于证据提供下一步协助”。每一步都读写同一份世界状态，故障原因和修复结果可追踪。
3. **补齐闭环所需的可靠性。**优先处理会破坏该切片的状态一致性问题：权限边界、tick 驱动、服务/进程生命周期、日志和存档恢复。以确定性状态推进和测试验证因果，不使用墙钟等待或只靠输出文本模拟成功。
4. **沿实际依赖扩展基础设施。**只有当下一项能力能支撑玩家目标或现有功能时，才扩展软件包/镜像、VPS/VM、NAS、邮件、聊天及其他设备和服务。每次只扩展一个可验收的能力链，验证故障、权限或配置变化会影响其上下游。
5. **在基础循环稳定后扩展安全玩法。**按“公开信息与侦察 → 受限访问 → 证据/日志 → 防守和追踪”推进；攻击与防守共用既有网络、账号、文件、进程、服务和经济状态，不做脱离世界状态的点击式小游戏。
6. **最后扩展规模和创作工具。**多人、组织、企业/ISP 网络、Web 客户端、可视化与编辑器属于更高阶段；先定义它们依赖的稳定接口和隔离边界，再实现，避免提前引入大范围架构改造。

### 每项工作的完成条件

* 先写清玩家可观察到的结果、涉及的真实状态、前置依赖和不在范围内的内容；沿用当前工作流的文件所有权，避免并行改同一核心文件。
* 给新增或修复的状态转移添加针对性测试；测试至少覆盖正常路径、一个相关失败/权限边界，以及失败修复后状态恢复。时间相关行为通过推进模拟 tick 验证。
* 用最小端到端测试证明命令、服务和下游效果连通；不要只以单个函数返回成功或输出文案作为验收。
* 仅运行覆盖本次变更的测试，再按影响范围运行 `go test ./...`、`go vet ./...` 或构建；修复本次改动引入的问题，不顺手扩展到无关子系统。
* 只有当玩家能发现入口、观察并验证原因、改变世界状态、看到可追踪后果，且权限和持久化没有被绕过时，才算完成。任何不能改变或读取真实模拟状态的装饰功能都不算完成。

因此，新功能的默认优先级是：先完成当前依赖链中的最小缺口，再扩展下一条链；不以命令数量、静态内容量或尚未验证的功能清单衡量进度。以上各阶段仍遵守本文件的硬约束：不执行真实宿主机代码，不制造无意义等待，不用随机灾害代替因果。
