---
name: web-xss
description: 跨站脚本(XSS)检测与利用:反射型、存储型、DOM 型。白盒追输入到 HTML/JS sink 的无转义路径,黑盒探反射与 DOM sink,带内用无害 PoC 证明执行。
---

# 跨站脚本(XSS)

不可信输入进入页面且未按上下文正确编码,导致脚本在受害者浏览器执行。

## 白盒信号

- **source**:参数/头/body/URL 片段/`location.*`/`document.referrer`/postMessage。
- **sink(HTML)**:模板里未转义输出(如 `|safe`、`v-html`、`dangerouslySetInnerHTML`、字符串拼 HTML)。
- **sink(DOM)**:`innerHTML`/`outerHTML`/`document.write`/`eval`/`setAttribute('href'|'src', 输入)`/`insertAdjacentHTML`。
- 关注输出**上下文**(HTML 文本 / 属性 / JS 字符串 / URL / CSS)与对应编码是否正确、CSP 是否存在且有效。

## 黑盒检测

- 反射型:参数回显到响应,试上下文对应的无害标记(先探 `'"<>` 是否被编码,再构造最小 payload)。
- DOM 型:看前端把哪些 source 写进危险 sink;用 `playwright-cli` 实测。
- 存储型:找"输入被保存并在某页面渲染"的路径——**注意 ROE**:存储型会落库(改数据),`safe_validate` 下不要真的持久化恶意 payload,改为依据代码/反射行为推断并标注,或仅在更高约束+审批下验证。

## 利用验证(按 ROE)

- 用**无害 PoC** 证明执行:`alert(document.domain)` 或读 `document.cookie` 的只读证明 + 截图;不要真窃取会话、不挂持久化攻击载荷。
- DOM/反射型用 `playwright-cli` 打开构造 URL,截图取证。
- 只走带内;不搭外部收集端点。

## 严重度要点

存储型、可打到他人会话 > 仅反射型需受害者点特制链接 > 只影响攻击者自己的会话(多半到"下限"、不该报)。按四问定级,注意第 1 问:数据是否真到了不该到的一方。

边界与报告规范见 `web-app-pentest/references/methodology.md`。
