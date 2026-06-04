export async function onRequest(context) {
  const { request } = context;
  const url = new URL(request.url);

  const target = `http://localhost:9000${url.pathname}${url.search}`;

  try {
    const resp = await fetch(target, {
      method: request.method,
      headers: request.headers,
      body: request.body,
    });
    return resp;
  } catch (e) {
    return new Response(JSON.stringify({ error: "proxy error", detail: e.message }), {
      status: 502,
      headers: { "Content-Type": "application/json" },
    });
  }
}
