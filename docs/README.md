# RAG Agent Powered Intelligent Q&A Platform

一个基于 [WeKnora](https://github.com/Tencent/WeKnora) 扩展的企业知识库问答系统。项目把文档解析、知识组织、混合检索和 Agent 推理连接成一条完整链路，用于构建可追溯、有依据的智能问答服务。

## 项目简介

系统支持将 PDF、Word、Excel、Markdown、图片等多种格式的资料导入知识库，并通过文档解析、分块、向量化和索引建立可检索的知识资产。用户可以通过 Web 界面或 API 提问，系统根据问题从文档、FAQ、Tag-Wiki 和知识图谱中召回内容，再交给 Agent 生成回答。

项目重点增强了以下能力：

- **Tag-Wiki 知识编译**：按知识标签组织内容，生成带证据和来源引用的 Wiki 页面。
- **四路混合检索**：融合向量检索、关键词检索、知识图谱检索和 Tag-Wiki 检索，并进行结果去重与排序。
- **FAQ 专用检索**：根据当前空间或知识库自动识别 FAQ 场景，优先返回匹配的问题和答案。
- **知识标签与关系图谱**：支持标签生命周期、标签关系、父子关系、同义关系和相关关系，并防止层级关系形成环。
- **ReAct Agent 问答**：支持多步推理、工具调用和知识库范围控制。
- **答案溯源与拒答**：回答关联原始文档、知识块或 Wiki 页面；检索不到有效依据时可以拒绝编造答案。
- **评测支持**：提供检索数据集、指标计算和 Tag-Wiki 检索样例，用于持续评估检索质量。

## 工作流程

```text
文档 / FAQ 导入
        │
        ▼
解析 → 分块 → 向量化 → 标签与 Wiki 编译 → 建立索引
        │
        ▼
用户问题 → 查询改写 → 四路检索 → RRF 融合与重排
        │
        ▼
带来源的 Agent 回答 / 无依据时拒答
```

## 快速启动

### 使用 Docker Compose

需要安装 Docker、Docker Compose 和 Git。

```bash
git clone https://github.com/wanghao-ship-it/RAG-Agent-Powered-Intelligent-Q-A-Platform.git
cd RAG-Agent-Powered-Intelligent-Q-A-Platform
cp .env.example .env
docker compose pull
docker compose up -d
```

启动后访问：

| 服务 | 地址 |
| --- | --- |
| Web 界面 | http://localhost |
| 后端 API | http://localhost:8080 |

首次启动后，请在 `.env` 中配置大语言模型、Embedding 模型、数据库和对象存储等服务。

### 本地 BGE-M3 Embedding（可选）

项目提供了一个兼容 OpenAI Embeddings 接口的本地 BGE-M3 服务：

```powershell
pip install -r requirements-embedding.txt
$env:BGE_MODEL_PATH = "模型目录路径"
python embedding_server.py
```

也可以使用 `start_bge_embedding.ps1` 启动。启动后，服务默认监听 `http://127.0.0.1:8090/v1`，具体模型名称和地址通过 `config/builtin_models.yaml` 或 `.env` 配置。

## 主要目录

| 目录 | 内容 |
| --- | --- |
| `cmd/` | 服务端、桌面端和迁移工具入口 |
| `internal/application/` | 知识库、标签、Wiki、检索和 Agent 业务逻辑 |
| `internal/taggraph/` | 标签关系和图谱规则 |
| `internal/evaluation/` | 数据集与检索评测指标 |
| `internal/agent/` | Agent 引擎和工具实现 |
| `migrations/` | SQLite 和其他数据库迁移脚本 |
| `config/` | 默认模型、Agent 和提示词配置 |
| `embedding_server.py` | 本地 BGE-M3 Embedding 服务 |
| `evalsets/` | 检索评测样例数据 |
| `frontend/` | Web 前端 |
| `docreader/` | 多格式文档解析服务 |
| `website-docs/` | 项目文档站点 |

## 配置说明

- `.env.example`：环境变量示例。复制为 `.env` 后按部署环境修改。
- `config/builtin_models.yaml`：声明默认的对话模型和向量模型，支持通过环境变量填充地址、密钥和模型名。
- `docker-compose.yml`：应用、数据库、向量检索和可选服务的编排配置。
- `migrations/`：数据库结构升级脚本，升级版本时应按顺序执行。

## 开发

后端主要使用 Go，文档解析和本地 Embedding 服务使用 Python，前端和文档站点使用 Node.js。后端代码修改后，可以按需执行：

```bash
go test ./internal/...
```

检索评测样例位于 `evalsets/`，指标实现位于 `internal/evaluation/`。生产部署前请根据实际模型、数据库和存储环境完善 `.env` 配置。

## 许可证

本项目基于 WeKnora 开源项目扩展，许可证和第三方依赖说明见 [LICENSE](./LICENSE) 与 [THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md)。
