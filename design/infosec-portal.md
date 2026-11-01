# InfoSec Portal 集成（可选）

把公司内部 **InfoSec Portal** 的工具类能力（如查 IP 归属/情报、查资产信息等）接入 ARTEX，
让 Agent 在测试过程中直接调用。**完全可选、由配置驱动**：不配置就等于没有这个功能，ARTEX 照常运行，
只是这些查询工具不可用。ARTEX 仍是独立项目，Portal 仅通过普通 HTTP 被引用。

> 对应方案：`design/INTEGRATION_PLAN.md` 工作流 C（第一阶段：Portal 工具）。

## 为什么用 HTTP 工具（而非 MCP）

每个 Portal 接口登记为一个原生 HTTP 工具，复用 ARTEX 自定义工具的 HTTP 执行器：直连、无额外进程、
无 MCP 往返，执行效率最优；且无需 Portal 先包一层 MCP。若将来 Portal 提供了 MCP 端点，也可继续用
ARTEX 的 `create_mcp` 接入，两者不冲突。

## 配置

在 `config.json` 增加 `infosec_portal` 段（示例见 `config.example.json` 的 `_example_infosec_portal`）：

```json
{
  "infosec_portal": {
    "base_url": "https://infosec.internal",
    "headers": { "Authorization": "Bearer <token>" },
    "insecure": false,
    "timeout_ms": 30000,
    "agents": ["planner", "worker", "auto"],
    "tools": [
      {
        "key": "portal_ip_lookup",
        "description": "查询某个 IP 的归属/情报信息。输入一个 IP，返回详细信息。",
        "method": "GET",
        "path": "/api/ip/{ip}",
        "schema": {
          "type": "object",
          "properties": { "ip": { "type": "string", "description": "要查询的 IP" } },
          "required": ["ip"]
        }
      }
    ]
  }
}
```

### 字段说明

| 字段 | 说明 |
|---|---|
| `base_url` | Portal 根地址。**留空或整段省略 = 功能关闭**。 |
| `headers` | 每次调用都带的静态头（鉴权等）。 |
| `insecure` | `true` 跳过 TLS 校验（内网自签证书）。 |
| `proxy` / `use_recording_proxy` | 可选出口代理 / 走 ARTEX 录制代理（默认都不走）。 |
| `timeout_ms` | 每次调用超时（默认 30000）。 |
| `agents` | 工具默认绑定的 agent（省略则 `planner`/`worker`/`auto`）。 |
| `tools[]` | 每项一个可调用工具，见下。 |

每个 `tools[]` 项：

| 字段 | 说明 |
|---|---|
| `key` | 工具名，小写字母开头，仅 `[a-z0-9_]`，须唯一。建议加 `portal_` 前缀。 |
| `description` | 给模型看的说明：**做什么 + 何时用**。写清楚能显著提升调用准确率。 |
| `method` | HTTP 方法（默认 GET）。 |
| `path` | 接在 `base_url` 后；可含 `{参数名}` 占位。 |
| `query` | 查询参数；值可含 `{参数名}`。 |
| `headers` | 该工具专属头；值可含 `{参数名}`。会与 `headers`（Portal 级）合并。 |
| `body` | 请求体；可含 `{参数名}`。 |
| `schema` | 该工具入参的 JSON Schema（定义有哪些 `{参数名}`）。 |
| `agents` | 覆盖默认绑定。 |
| `enabled` | `false` 跳过该工具。 |

`{参数名}` 在运行时用工具调用的入参填充，占位名需与 `schema.properties` 对应。

### 用环境变量注入密钥（推荐）

避免把 token 写进文件：

```bash
export ARTEX_INFOSEC_PORTAL_BASE_URL="https://infosec.internal"
export ARTEX_INFOSEC_PORTAL_TOKEN="xxxxx"   # 自动注入 Authorization: Bearer xxxxx
```

## 运行时行为

- 启动时若配置了 `infosec_portal.base_url`，每个工具会 seed 一行进工具表（`system` 工具），
  之后可在「工具管理」界面按 agent 调整绑定 / 启停——即"可配置调整"。
- 未配置：不注册、不 seed，零影响。
- 从配置里删掉某工具：界面上可能残留一行旧记录，但没有实现支撑，调用不到，属无害残留。

## 域名清单 + 场景化发起测试（已实现）

- **域名清单**：页面「域名清单」(`/function/inventory`)。
  - 数据源：配置 `infosec_portal.inventory.path` 指向 Portal 列 inventory 的接口；
    **未配置则回落到内置 mock 数据**,功能可先用起来。返回解析宽松(数组或 `{data|domains|result|items:[...]}`,
    字段名 `domain/host/name`、`company/org/owner`、`ips/ip`、`tags` 皆可)。
  - 「同步到资产库」把清单域名/IP 入库(参照 ScopeSentry),可选自动建公司 + scope。
- **场景(scenarios)**：预制测试意图,复用「任务模板」。启动时幂等 seed 若干内置场景
  (Web 常规、API 侦察、认证专项、越权/IDOR、SSRF、白盒审计),用户也可在「任务模板」里自行增删。
- **一键发起**：清单里点域名 →「发起测试」→ 勾选场景(可多选)+ 可选补充说明 → 建任务,
  免去每次手写任务描述。所选场景的 goal 会合并、叠加补充说明,拦截规则做并集。

### 相关接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/inventory/domains` | 列域名清单(真实或 mock) |
| POST | `/api/inventory/sync` | 同步清单 → 资产库(`{domains?, create_company?}`) |
| GET | `/api/scenarios` | 列场景(任务模板) |
| POST | `/api/scenarios/launch` | 场景化建任务(`{target, scenario_ids[], extra?, name?, ...}`) |
