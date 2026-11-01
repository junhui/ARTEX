---
name: web-injection
description: 注入类漏洞的检测与利用打法:SQL 注入、命令注入、LFI/RFI、SSTI(模板注入)、路径穿越、不安全反序列化。白盒追 source→sink,黑盒看行为,带内出最小 PoC。
---

# 注入类(Injection)

不可信输入未经充分处理到达危险 sink:SQL 查询、Shell 命令、文件操作、模板引擎、反序列化。

## 白盒信号(有源码时,优先)

- **source**:请求参数 / 查询串 / 头 / body / 路由段 / 文件名 / 反序列化入口。
- **sink**:字符串拼接的 SQL、`exec/system/popen`、`open/读文件(路径含输入)`、模板 `render(字符串含输入)`、`pickle/unserialize/ObjectInputStream` 等。
- 找"输入→sink 之间没有参数化/白名单/转义/类型约束"的路径。ORM 的 raw/原生查询、动态表名列名尤其可疑。

## 黑盒检测

- 对每个参数试探错误/布尔/时间差异:SQL(引号、`' OR '1'='1`、布尔/时间盲注)、命令(`;id`、`|`、`$()` 的只读探针)、
  SSTI(`{{7*7}}`/`${7*7}` 等按引擎)、路径穿越(`../` 读可证明的非敏感文件)、LFI(包含已知无害文件)。
- 先用**只读、无害**探针判存在性,再谈利用。

## 利用验证(按 ROE)

- `safe_validate`(严格不改数据,默认):只做**只读**证明——SQLi 用只读 `SELECT`/布尔/时间盲注证明可控,严禁 `UPDATE/DELETE/DROP`、写文件、落 webshell;命令注入用无害只读命令(如回显一个固定串)证明执行;路径穿越读一个可证明越权但非敏感的文件。
- 更高约束(standard/intrusive)下的改动型验证,**先审批**再做,并选影响最小方式。
- 只走带内;需要带外(如盲注外连 DNS)才能证的,记 `blocked` + 代码证据。

## 严重度要点

SQLi/命令注入通常后果重(数据面/服务器),但仍按"实际拿到什么 + 代价 + 波及 + 本系统价值"四问定级(见 methodology)。只证到"参数可控/有报错"而没拿到实际数据或执行,别直接报 critical。

边界与报告规范见 `web-app-pentest/references/methodology.md`。
