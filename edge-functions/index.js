const GEMINI_BASE = "https://generativelanguage.googleapis.com/v1beta";

// Token pool
let tokenPool = [];
let tokenCooldown = {};

function initTokens(env) {
  for (const [k, v] of Object.entries(env)) {
    if (k.startsWith("TOKEN") && v) tokenPool.push(v);
  }
}

function getToken() {
  const now = Date.now();
  for (const t of tokenPool) {
    if (!tokenCooldown[t] || now > tokenCooldown[t]) {
      delete tokenCooldown[t];
      return t;
    }
  }
  return null;
}

function markRateLimited(token) {
  tokenCooldown[token] = Date.now() + 60000;
}

// Auth
function checkAuth(request, env) {
  const apiKey = env.APIKEY;
  if (!apiKey) return false;
  const auth = request.headers.get("Authorization") || "";
  return auth === `Bearer ${apiKey}`;
}

// Parse data URL
function parseDataURL(url) {
  if (!url.startsWith("data:")) throw new Error("not a data URL");
  const comma = url.indexOf(",");
  const media = url.substring(5, comma);
  const data = url.substring(comma + 1);
  let mime = "image/png";
  if (media.includes(";base64")) mime = media.split(";base64")[0];
  else if (media) mime = media;
  return { data, mime };
}

// Chat -> Gemini conversion
function chatToGemini(req) {
  let systemParts = [];
  let contents = [];
  for (const msg of req.messages || []) {
    if (msg.role === "system") {
      systemParts.push({ text: typeof msg.content === "string" ? msg.content : "" });
      continue;
    }
    let role = msg.role === "assistant" ? "model" : msg.role;
    let parts = [];
    if (typeof msg.content === "string") {
      parts.push({ text: msg.content });
    } else if (Array.isArray(msg.content)) {
      for (const p of msg.content) {
        if (p.type === "text") parts.push({ text: p.text });
        else if (p.type === "image_url" && p.image_url) {
          const { data, mime } = parseDataURL(p.image_url.url);
          parts.push({ inlineData: { mimeType: mime, data } });
        }
      }
    }
    if (parts.length) contents.push({ role, parts });
  }
  const gemReq = { contents };
  if (systemParts.length) gemReq.systemInstruction = { parts: systemParts };
  if (req.temperature != null || req.max_tokens != null || req.top_p != null) {
    gemReq.generationConfig = {};
    if (req.temperature != null) gemReq.generationConfig.temperature = req.temperature;
    if (req.max_tokens != null) gemReq.generationConfig.maxOutputTokens = req.max_tokens;
    if (req.top_p != null) gemReq.generationConfig.topP = req.top_p;
  }
  if (req.tools) {
    gemReq.tools = [];
    for (const t of req.tools) {
      if (t.type === "function" && t.function) {
        gemReq.tools.push({ functionDeclarations: [t.function] });
      }
    }
  }
  return { gemReq, model: req.model || "gemini-2.5-flash" };
}

// Gemini response -> Chat response
function geminiToChat(gemResp, model) {
  const choices = (gemResp.candidates || []).map((c, i) => ({
    index: i,
    message: { role: "assistant", content: extractText(c.content) },
    finish_reason: mapFinish(c.finishReason),
  }));
  const resp = {
    id: "chatcmpl-" + Date.now(),
    object: "chat.completion",
    created: Math.floor(Date.now() / 1000),
    model,
    choices,
  };
  if (gemResp.usageMetadata) {
    resp.usage = {
      prompt_tokens: gemResp.usageMetadata.promptTokenCount,
      completion_tokens: gemResp.usageMetadata.candidatesTokenCount,
      total_tokens: gemResp.usageMetadata.totalTokenCount,
    };
  }
  return resp;
}

function extractText(content) {
  return (content?.parts || []).map(p => p.text || "").join("");
}

function mapFinish(r) {
  switch (r) {
    case "STOP": return "stop";
    case "MAX_TOKENS": return "length";
    case "SAFETY": case "RECITATION": return "content_filter";
    default: return "stop";
  }
}

// Responses -> Gemini
function responsesToGemini(req) {
  const gemReq = {};
  if (req.instructions) {
    gemReq.systemInstruction = { parts: [{ text: req.instructions }] };
  }
  let contents = [];
  if (typeof req.input === "string") {
    contents = [{ role: "user", parts: [{ text: req.input }] }];
  } else if (Array.isArray(req.input)) {
    for (const m of req.input) {
      if (m.role === "system") continue;
      let role = m.role === "assistant" ? "model" : m.role;
      let parts = typeof m.content === "string" ? [{ text: m.content }] : [];
      if (parts.length) contents.push({ role, parts });
    }
  }
  gemReq.contents = contents;
  if (req.temperature != null || req.max_output_tokens != null || req.top_p != null) {
    gemReq.generationConfig = {};
    if (req.temperature != null) gemReq.generationConfig.temperature = req.temperature;
    if (req.max_output_tokens != null) gemReq.generationConfig.maxOutputTokens = req.max_output_tokens;
    if (req.top_p != null) gemReq.generationConfig.topP = req.top_p;
  }
  if (req.tools) {
    gemReq.tools = [];
    for (const t of req.tools) {
      if (t.type === "function") {
        gemReq.tools.push({ functionDeclarations: [{ name: t.name, description: t.description, parameters: t.parameters }] });
      }
    }
  }
  return { gemReq, model: req.model || "gemini-2.5-flash" };
}

// Gemini -> Responses
function geminiToResponses(gemResp, model) {
  const output = (gemResp.candidates || []).map(c => ({
    id: "msg-" + Date.now(),
    type: "message",
    status: "completed",
    role: "assistant",
    content: [{ type: "output_text", text: extractText(c.content) }],
  }));
  const resp = {
    id: "resp-" + Date.now(),
    object: "response",
    created_at: Math.floor(Date.now() / 1000),
    model,
    output,
  };
  if (gemResp.usageMetadata) {
    resp.usage = {
      input_tokens: gemResp.usageMetadata.promptTokenCount,
      output_tokens: gemResp.usageMetadata.candidatesTokenCount,
      total_tokens: gemResp.usageMetadata.totalTokenCount,
    };
  }
  return resp;
}

// Gemini API call
async function callGemini(path, body) {
  const token = getToken();
  if (!token) throw new Error("no available tokens");
  const url = `${GEMINI_BASE}${path}?key=${token}`;
  const resp = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (resp.status === 429) {
    markRateLimited(token);
    throw new Error("rate limited");
  }
  return resp;
}

async function callGeminiWithRetry(path, body, maxRetries = 2) {
  for (let i = 0; i <= maxRetries; i++) {
    try { return await callGemini(path, body); }
    catch (e) { if (i >= maxRetries) throw e; }
  }
}

// JSON response helper
function json(data, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function errorJson(msg, status = 500) {
  return json({ error: { message: msg, type: "server_error" } }, status);
}

// Route matching
const ROUTES = {
  chat: { method: "POST", path: "/v1/chat/completions" },
  responses: { method: "POST", path: "/v1/responses" },
  models: { method: "GET", path: "/v1/models" },
  geminiGenerate: { prefix: "/v1beta/models/", suffix: ":generateContent" },
  geminiStream: { prefix: "/v1beta/models/", suffix: ":streamGenerateContent" },
};

function matchRoute(method, pathname) {
  if (method === "POST" && pathname === "/v1/chat/completions") return "chat";
  if (method === "POST" && pathname === "/v1/responses") return "responses";
  if (method === "GET" && pathname === "/v1/models") return "models";
  if (method === "POST" && pathname.includes(":generateContent")) {
    if (pathname.endsWith(":streamGenerateContent")) return "geminiStream";
    return "geminiGenerate";
  }
  return null;
}

// Main handler
export async function onRequest(context) {
  const { request, env } = context;
  const url = new URL(request.url);
  const method = request.method;
  const pathname = url.pathname;

  // Init tokens on first request
  if (tokenPool.length === 0) initTokens(env);

  // Landing page
  if (method === "GET" && (pathname === "/" || pathname === "")) {
    return json({
      service: "Gemini EdgeOne Proxy",
      endpoints: {
        chat_completions: "POST /v1/chat/completions",
        responses: "POST /v1/responses",
        gemini_generate: "POST /v1beta/models/{model}:generateContent",
        gemini_stream: "POST /v1beta/models/{model}:streamGenerateContent",
        models: "GET /v1/models",
      },
      docs: "https://github.com/leemwood/gemini-edgeone-proxy",
    });
  }

  if (method === "GET" && pathname === "/health") {
    return new Response("ok");
  }

  // Check auth for API routes
  if (!checkAuth(request, env)) {
    return errorJson("unauthorized", 401);
  }

  const route = matchRoute(method, pathname);
  if (!route) {
    return errorJson("not found", 404);
  }

  try {
    if (route === "models") {
      const token = getToken();
      if (!token) return errorJson("no available tokens", 503);
      const resp = await fetch(`${GEMINI_BASE}/models?key=${token}`);
      if (resp.status === 429) { markRateLimited(token); return errorJson("rate limited", 429); }
      const data = await resp.json();
      const models = (data.models || []).map(m => ({
        id: m.name.replace("models/", ""),
        object: "model",
        created: 1686935002,
        owned_by: "google",
      }));
      return json({ object: "list", data: models });
    }

    if (route === "geminiGenerate" || route === "geminiStream") {
      const isStream = route === "geminiStream";
      // Parse model from path: /v1beta/models/{model}:xxx
      const modelPart = pathname.replace("/v1beta/models/", "");
      const model = modelPart.split(":")[0];
      const geminiPath = isStream
        ? `/models/${model}:streamGenerateContent?alt=sse`
        : `/models/${model}:generateContent`;

      const body = await request.json();
      const resp = await callGeminiWithRetry(geminiPath, body);
      return new Response(resp.body, {
        status: resp.status,
        headers: resp.headers,
      });
    }

    if (route === "chat") {
      const req = await request.json();
      const isStream = req.stream;
      const { gemReq, model } = chatToGemini(req);
      const geminiPath = isStream
        ? `/models/${model}:streamGenerateContent?alt=sse`
        : `/models/${model}:generateContent`;

      const resp = await callGeminiWithRetry(geminiPath, gemReq);
      if (!isStream) {
        const gemResp = await resp.json();
        const chatResp = geminiToChat(gemResp, model);
        return json(chatResp);
      }
      // Stream: convert Gemini SSE chunks to OpenAI SSE format
      const chunkId = "chatcmpl-" + Date.now();
      const created = Math.floor(Date.now() / 1000);
      const { readable, writable } = new TransformStream();
      const writer = writable.getWriter();
      const encoder = new TextEncoder();
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();

      (async () => {
        let buffer = "";
        try {
          while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split("\n");
            buffer = lines.pop() || "";
            for (const line of lines) {
              if (!line.startsWith("data: ")) continue;
              const data = line.substring(6);
              if (!data) continue;
              try {
                const gemChunk = JSON.parse(data);
                const text = extractText(gemChunk.candidates?.[0]?.content || {});
                const finish = mapFinish(gemChunk.candidates?.[0]?.finishReason || "");
                const chunk = {
                  id: chunkId,
                  object: "chat.completion.chunk",
                  created,
                  model,
                  choices: [{
                    index: 0,
                    delta: text ? { content: text } : {},
                    finish_reason: finish || null,
                  }],
                };
                writer.write(encoder.encode(`data: ${JSON.stringify(chunk)}\n\n`));
              } catch {}
            }
          }
          writer.write(encoder.encode("data: [DONE]\n\n"));
        } catch {}
        try { writer.close(); } catch {}
      })();

      return new Response(readable, {
        headers: {
          "Content-Type": "text/event-stream",
          "Cache-Control": "no-cache",
          "Connection": "keep-alive",
        },
      });
    }

    if (route === "responses") {
      const req = await request.json();
      const { gemReq, model } = responsesToGemini(req);
      const geminiPath = `/models/${model}:generateContent`;
      const resp = await callGeminiWithRetry(geminiPath, gemReq);
      const gemResp = await resp.json();
      return json(geminiToResponses(gemResp, model));
    }
  } catch (e) {
    return errorJson(e.message, 502);
  }
}
