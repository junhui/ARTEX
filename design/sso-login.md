# SSO 登录接管（工作流 B）

让需要 **SSO 登录**（可能含 MFA）的内部系统也能被测试：人工在**服务端有头浏览器**里经 noVNC 完成登录，
Agent 随后复用已认证会话继续测试。**可选**：`browser_login.enabled=false`（默认）时整套功能关闭，ARTEX 不受影响。

## 组成

| 层 | 文件 | 说明 |
|---|---|---|
| 基础设施 | `scripts/browser-session.sh`、`start.sh`、`Dockerfile` | `ARTEX_BROWSER_LOGIN=1` 时启动 Xvfb + x11vnc + noVNC(websockify) + 有头 Chromium(带 CDP 端口 9222)，noVNC 暴露在 6080 |
| CDP 助手 | `skills/authenticated-session/scripts/cdp-helper.js` | Node+playwright：经 CDP `navigate` / `export-state` 操作那个有头 Chromium |
| 后端 | `server/browser_login.go` | 阻塞式 `browser_login` 工具 + 待处理接管管理 + API |
| 配置 | `config.BrowserLogin` | `enabled / novnc_url / cdp_endpoint / timeout_sec`，含环境变量覆盖 |
| 技能 | `skills/authenticated-session/SKILL.md` | 指导 Agent 何时/如何用 `browser_login` 并复用会话态 |
| 前端 | `web/.../_components/browser-login-panel.tsx` | 轮询待处理接管 → 弹出 noVNC 面板 + “我已登录完成” |

## 流程

1. Agent 遇到登录墙 → 调 `browser_login{login_url, success_hint?}`。
2. 后端经 CDP 把有头 Chromium 导航到 `login_url`，登记一个“待登录接管”并**阻塞等待**。
3. 前端面板弹出 noVNC（iframe 到 `novnc_url`），人工完成 SSO/MFA，点“我已登录完成”
   → `POST /api/browser-login/{taskID}/done`。
4. 后端经 CDP 导出 `storageState` 到 `tasks/<id>/auth-state.json`，解除阻塞，返回路径给 Agent。
5. Agent 用 `playwright-cli state-load <path>` 复用已认证会话继续测试。会话过期可再次接管。

## 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/browser-login/pending` | 列待处理接管（含 `enabled`、noVNC URL） |
| POST | `/api/browser-login/{id}/done` | 人工登录完成 |
| POST | `/api/browser-login/{id}/cancel` | 取消接管 |

## 开启步骤（部署）

1. 用含 VNC 栈的新镜像（Dockerfile 已加 `xvfb x11vnc novnc websockify chromium`）。
2. 容器以 `ARTEX_BROWSER_LOGIN=1` 启动（`start.sh` 会拉起 `scripts/browser-session.sh`）。
3. `config.json` 设 `browser_login.enabled=true`，`novnc_url` 填运维可访问的 noVNC 地址
   （默认 noVNC 在 6080，如 `http://<host>:6080/vnc.html?autoconnect=1&resize=remote`）。

## ⚠️ 需在真实环境验证

VNC/Xvfb/Chromium 这套基础设施**无法在本仓库 CI/沙箱里端到端测试**（无显示设备）。
已验证：Go 编译/单测、前端 tsc/biome、各进程命令与参数。仍需在真实部署里验证：
Xvfb/x11vnc/websockify 起得来、noVNC 可访问、CDP 导航与 `storageState` 导出、worker 复用会话。
