#!/bin/sh
# 服务端有头浏览器 + noVNC(方案 工作流 B:SSO 登录接管)。
#
# 仅在 ARTEX_BROWSER_LOGIN=1 时由 start.sh 调用。启动 Xvfb、x11vnc、noVNC(websockify)
# 与一个带 CDP 调试端口的有头 Chromium;ARTEX 经 CDP 驱动该浏览器,人工经 noVNC 完成登录。
# best-effort、尽量幂等;缺依赖时安静退出,不影响主程序。
#
# ⚠️ 需在真实部署环境验证(本仓库 CI/沙箱无显示设备,无法端到端测试)。
#
# 可调环境变量(均有默认):
#   ARTEX_BL_DISPLAY(:99) ARTEX_BL_VNC_PORT(5900) ARTEX_BL_NOVNC_PORT(6080)
#   ARTEX_BL_CDP_PORT(9222) ARTEX_BL_SCREEN(1280x800x24) ARTEX_BL_CHROME(chromium)
set -u

DISPLAY_NUM="${ARTEX_BL_DISPLAY:-:99}"
VNC_PORT="${ARTEX_BL_VNC_PORT:-5900}"
NOVNC_PORT="${ARTEX_BL_NOVNC_PORT:-6080}"
CDP_PORT="${ARTEX_BL_CDP_PORT:-9222}"
SCREEN="${ARTEX_BL_SCREEN:-1280x800x24}"
CHROME_BIN="${ARTEX_BL_CHROME:-chromium}"
export DISPLAY="$DISPLAY_NUM"

command -v Xvfb >/dev/null 2>&1 || { echo "[browser-session] Xvfb 未安装,跳过(SSO 登录接管不可用)"; exit 0; }

# Xvfb 虚拟显示
pgrep -f "Xvfb $DISPLAY_NUM" >/dev/null 2>&1 || {
	Xvfb "$DISPLAY_NUM" -screen 0 "$SCREEN" >/tmp/bl-xvfb.log 2>&1 &
	sleep 1
}

# x11vnc 把显示暴露为 VNC
if command -v x11vnc >/dev/null 2>&1; then
	pgrep -f "x11vnc.*-rfbport $VNC_PORT" >/dev/null 2>&1 || \
		x11vnc -display "$DISPLAY_NUM" -nopw -forever -shared -rfbport "$VNC_PORT" -bg >/tmp/bl-x11vnc.log 2>&1
fi

# noVNC(websockify 提供 web 端 VNC 客户端)
if command -v websockify >/dev/null 2>&1; then
	NOVNC_WEB=""
	for d in /usr/share/novnc /usr/share/webapps/novnc; do
		[ -d "$d" ] && NOVNC_WEB="$d" && break
	done
	pgrep -f "websockify.*$NOVNC_PORT" >/dev/null 2>&1 || {
		if [ -n "$NOVNC_WEB" ]; then
			websockify --web="$NOVNC_WEB" "$NOVNC_PORT" "localhost:$VNC_PORT" >/tmp/bl-novnc.log 2>&1 &
		else
			websockify "$NOVNC_PORT" "localhost:$VNC_PORT" >/tmp/bl-novnc.log 2>&1 &
		fi
	}
fi

# 有头 Chromium + CDP 调试端口(ARTEX 经此端口驱动)
command -v "$CHROME_BIN" >/dev/null 2>&1 || CHROME_BIN="chromium-browser"
if command -v "$CHROME_BIN" >/dev/null 2>&1; then
	pgrep -f "remote-debugging-port=$CDP_PORT" >/dev/null 2>&1 || \
		"$CHROME_BIN" --no-sandbox --disable-dev-shm-usage \
			--remote-debugging-port="$CDP_PORT" --remote-debugging-address=127.0.0.1 \
			--user-data-dir=/tmp/bl-chrome --no-first-run --no-default-browser-check \
			--window-size=1280,800 about:blank >/tmp/bl-chromium.log 2>&1 &
else
	echo "[browser-session] 未找到 chromium,跳过浏览器启动"
fi

echo "[browser-session] noVNC=:$NOVNC_PORT  CDP=:$CDP_PORT  display=$DISPLAY_NUM"
