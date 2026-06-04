# gemini-edgeone-proxy

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![EdgeOne Pages](https://img.shields.io/badge/deploy-EdgeOne%20Pages-0052CC)](https://pages.edgeone.ai)

将 Gemini API 包装为多格式代理，一键部署到 EdgeOne Pages 全球节点，绕过地域限制。

```mermaid
flowchart LR
    Client[客户端] -->|Bearer APIKEY| Proxy[gemini-edgeone-proxy<br/>EdgeOne Pages]
    Proxy -->|TOKEN1 / TOKEN2 轮询| Gemini[Google Gemini API]
    Gemini -->|SSE / JSON| Proxy
    Proxy -->|OpenAI 旧格式<br/>OpenAI 新格式<br/>Gemini 原生格式| Client
```

## Quick Start

```bash
# 1. 设置环境变量
export APIKEY=sk-your-aggregate-key
export TOKEN1=AIzaSy...your-real-key-1
export TOKEN2=AIzaSy...your-real-key-2

# 2. 部署到 EdgeOne Pages
# 将 cloud-functions/ 目录上传，平台自动构建 GOOS=linux GOARCH=amd64

# 3. 调用
curl https://your-project.edgeone.app/v1/chat/completions \
  -H "Authorization: Bearer sk-your-aggregate-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"Hello"}]}'
```

## 解决什么问题

- **地域封锁**: Google Gemini API 在部分区域不可用，代理部署在 EdgeOne 可用节点后即绕过限制
- **多 Key 负载**: 支持多个 Gemini API Key 轮询，遇 429 自动切换冷却 60s
- **格式兼容**: 提供三种输出格式，客户端零改动接入
- **零依赖**: 纯 Go 标准库，编译后约 8MB，冷启动 < 100ms

## 端点

| 方法 | 路径 | 格式 |
|------|------|------|
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions (旧格式，含 SSE 流式) |
| `POST` | `/v1/responses` | OpenAI Responses API (新格式) |
| `POST` | `/v1beta/models/{model}:generateContent` | Gemini 原生 (非流式) |
| `POST` | `/v1beta/models/{model}:streamGenerateContent` | Gemini 原生 (SSE 流式直通) |
| `GET` | `/v1/models` | 模型列表 (OpenAI 格式) |

## 配置

所有配置通过环境变量注入，在 EdgeOne Pages 控制台设置：

| 变量 | 说明 |
|------|------|
| `APIKEY` | 用户持有的聚合密钥，仅做校验，**不发给 Google** |
| `TOKEN1` | 真实 Gemini API Key，轮询使用 |
| `TOKEN2` | 第二个 API Key，遇限流自动切换 |
| `TOKEN`*N* | 支持任意数量 `TOKEN` 前缀变量 |

## 格式转换

### OpenAI Chat Completions (旧格式)

```
POST /v1/chat/completions
```

请求体为标准 OpenAI Chat Completions 格式，代理内部完成：

- `messages[role=system]` -> `systemInstruction`
- `messages[role=user/assistant]` -> `contents[]` (assistant -> model)
- `tools[type=function]` -> `functionDeclarations`
- `stream: true` -> Gemini SSE 流式，逐 chunk 转回 OpenAI delta 格式
- 图片 `image_url` (data URL) -> Gemini `inlineData`

### OpenAI Responses API (新格式)

```
POST /v1/responses
```

- `instructions` -> `systemInstruction`
- `input` (string 或 消息数组) -> `contents[]`
- 响应 `output[]` Items 从 Gemini candidates 重建

### Gemini 原生格式

```
POST /v1beta/models/gemini-2.5-flash:generateContent
POST /v1beta/models/gemini-2.5-flash:streamGenerateContent
```

认证后请求体直接透传，响应直通（包括 SSE 流式）。

## Token 管理策略

- **首次请求**: 按 `TOKEN1` -> `TOKEN2` -> ... 顺序轮询
- **遇 429**: 该 Token 进入 60s 冷却，自动切换下一个
- **全部冷却**: 返回 `503 Service Unavailable`，等待恢复
- **无状态**: 不依赖外部存储，冷启动即从 TOKEN1 开始

## 本地测试

```bash
cd cloud-functions
APIKEY=sk-test TOKEN1=your-real-key go run .

# 测试各端点
curl -H "Authorization: Bearer sk-test" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}' \
  http://localhost:8080/v1/chat/completions
```

```bash
# 运行测试
go test -v .
```

## 部署到 EdgeOne Pages

1. 登录 [EdgeOne Pages 控制台](https://console.cloud.tencent.com/edgeone/pages)
2. 创建项目，关联此仓库
3. 在「环境变量」中添加 `APIKEY`、`TOKEN1`、`TOKEN2`
4. 构建配置：框架预设选择「Go」，输出目录留空
5. 部署区域选择**新加坡/日本/美国**等 Gemini 可用节点
6. 部署完成，使用分配的域名访问

## License

MIT
