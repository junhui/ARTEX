---
name: authenticated-session
description: 目标需要登录(尤其 SSO/MFA)且无法用静态凭据自动登录时,用它发起"人工登录接管"——人在服务端内置浏览器里完成登录,随后复用已认证会话继续测试。
---

# 认证会话 / SSO 登录接管

很多内部系统需要 **SSO 登录**(可能含 MFA / 推送 / 账号选择),无法用账号密码自动登录。
本 skill 让你把登录这一步**交给人工**:人在服务端内置浏览器(经 noVNC 可视化操作)里完成登录,
你随后拿到已认证的会话态继续测试。

## 何时用

- 打开目标即跳转登录页 / 返回 401 / 需要 SSO,且你没有可用的会话。
- 任务目标是登录后的功能,而当前浏览器未登录。

**不要**向用户索要明文账号密码/OTP 去自己登录;走本 skill 的人工接管。

## 流程

1. 调用 `browser_login` 工具,传入 `login_url`(登录入口 URL),可选 `success_hint`(登录成功的判据,如登录后 URL 片段或页面元素文案)。
   - 该工具会:在服务端内置浏览器打开 `login_url`,并**阻塞等待人工**在 noVNC 面板里完成登录。
   - 前端会出现"登录接管"面板:人工点开 noVNC、完成 SSO/MFA、点"我已登录完成"。
2. 人工完成后,工具会**导出已认证会话态**(storage-state)到任务目录并返回其路径(形如 `tasks/<id>/auth-state.json`)。
3. 之后用 `playwright-cli` 复用该会话态继续测试:

   ```bash
   # 复用已认证会话(storage-state)
   playwright-cli state-load <工具返回的路径>
   playwright-cli open <目标登录后页面>
   playwright-cli snapshot
   ```

   或使用命名会话 `-s=task<id>` 保持上下文。

## 注意

- 仅对**已授权**目标使用。
- 会话态文件按任务隔离存放,**不要**把它或其中的 cookie/token 写进报告、日志或发给外部。
- 会话可能过期;过期后可再次调用 `browser_login` 重新接管。
- 若工具返回"未启用服务端登录接管",说明部署未开启该功能(`browser_login.enabled`),
  改用其他已授权的访问方式或告知用户开启。

脚本:`scripts/cdp-helper.js` 由后端在导航/导出会话态时调用(连 CDP 操作内置浏览器),一般无需你直接运行。
