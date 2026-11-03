# 本地运行 ARTEX（含本分支改动）并用本地/免费模型试玩

> 云端沙箱无法暴露端口给你的浏览器,也下载不了本地模型(出网受限),所以在**你自己的机器**上跑。
> 仓库自带的 `docker-compose.yml` 拉的是上游镜像(不含本分支改动),因此用下面的**源码构建**方式。

## 方式 A：Docker 一键(从本分支源码构建,免装 Go/Node)

```bash
git clone -b claude/peaceful-faraday-ircrhb https://github.com/junhui/ARTEX.git
cd ARTEX
cp .env.example .env    # 然后编辑 .env(见下)
docker compose -f docker-compose.yml -f docker-compose.build.yml up --build
```

首次构建会装 Chromium 等,耗时数分钟。起来后打开 **http://localhost:8787**,设管理员密码即可玩。

`.env` 至少设置:

```bash
POSTGRES_PASSWORD=change-me
# —— LLM 三选一,见下文 ——
```

## 方式 B：不用 Docker（install.sh 本地编译）

需要 Go ≥1.26 + Node,Postgres 可让脚本用 Docker 起:

```bash
git clone -b claude/peaceful-faraday-ircrhb https://github.com/junhui/ARTEX.git
cd ARTEX && ./install.sh    # 选 2) 本地运行
```

---

## 配 LLM（任选其一，填进 `.env`）

### ① 本地 Qwen3-14B（Ollama，推荐用于内部系统——数据不出本机）

在你机器上(非容器)装 Ollama 并拉模型:

```bash
# https://ollama.com/download  安装后:
ollama pull qwen3:14b     # 约 9GB;建议 ≥16GB 内存
# ollama 一般随安装常驻;如未启动: ollama serve
```

`.env`（Docker 方式 A — 容器经 host.docker.internal 访问宿主 Ollama）:

```bash
ARTEX_LLM_PROVIDER=openai
ARTEX_LLM_BASE_URL=http://host.docker.internal:11434/v1
ARTEX_LLM_MODEL=qwen3:14b
OPENAI_API_KEY=ollama        # 任意非空字符串即可,Ollama 不校验
```

> 本地编译(方式 B)则把 BASE_URL 换成 `http://127.0.0.1:11434/v1`。
> Ollama 暴露的是 OpenAI 兼容端点 `/v1`。

### ② OpenRouter 免费模型（无需下载，联网即用）

```bash
ARTEX_LLM_PROVIDER=openai
ARTEX_LLM_BASE_URL=https://openrouter.ai/api/v1
ARTEX_LLM_MODEL=google/gemma-3-27b-it:free   # 注意:不是 gemma-4-31b(不存在)
OPENAI_API_KEY=sk-or-xxxxx                    # 你的 OpenRouter key
```

> ⚠️ 免费公网模型会把目标信息/流量发给第三方——**只用公开/测试靶场**,别测真实内部系统。
> 测内部系统请用 ①(本地 Ollama,数据留在本机)或自托管模型。

### ③ 也可在 UI 里配

不写 `.env` 的 LLM,启动后进 **系统 → LLM → 新建**:格式 OpenAI、填 Base URL / Key / 模型即可。

---

## 快速试玩路径

1. 打开 http://localhost:8787,设密码登录。
2. 左侧 **域名清单**:有内置 mock 域名 → 点「发起测试」→ 选场景 + 约束等级 + 补充说明 → 建任务。
3. **任务** 里看执行过程(配了 LLM 才会真正跑);**发现 / 报告** 看结果展示(报告页可导出 HTML/PDF)。
4. 想试 InfoSec Portal 工具 / 真实域名清单,在 `config.json` 配 `infosec_portal`(见 `design/infosec-portal.md`)。

> 说明:本分支的 UI/功能改动我已在沙箱里用本地编译(`npm run build:static` + `go build -tags embedui`)验证过;
> Docker 源码构建(方式 A)复用同样的构建命令,但沙箱出网受限我无法在此实跑 docker build——在你机器上按标准流程应可直接构建。
