# Gemini EdgeOne Proxy

基于 EdgeOne Pages Go 运行时的 Gemini API 代理，支持多格式转发。

## 端点

| 方法 | 路径 | 格式 |
|------|------|------|
| POST | `/v1/chat/completions` | OpenAI Chat Completions (旧格式) |
| POST | `/v1/responses` | OpenAI Responses API (新格式) |
| POST | `/v1beta/models/{model}:generateContent` | Gemini 原生 |
| POST | `/v1beta/models/{model}:streamGenerateContent` | Gemini 原生 (流式) |
| GET | `/v1/models` | 模型列表 (OpenAI 格式) |

## 认证

客户端使用 `Authorization: Bearer <APIKEY>` 请求头。

## 环境变量

在 EdgeOne Pages 控制台配置:

| 变量 | 说明 |
|------|------|
| `APIKEY` | 用户持有的聚合令牌，不发给 Google |
| `TOKEN1` | 真实 Gemini API Key，轮询使用 |
| `TOKEN2` | 第二个 Gemini API Key |

支持任意数量的 `TOKEN` 前缀环境变量 (TOKEN1, TOKEN2, TOKEN3...)。

## 部署到 EdgeOne Pages

1. 将 `cloud-functions/` 目录部署到 EdgeOne Pages
2. 在控制台添加上述环境变量
3. 选择新加坡/日本/美国等 Gemini 可用区域的节点

## 本地测试

```bash
cd cloud-functions
APIKEY=sk-test TOKEN1=your-key go run .
curl -H "Authorization: Bearer sk-test" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"Hello"}]}' \
  http://localhost:8080/v1/chat/completions
```
