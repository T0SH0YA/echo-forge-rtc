// POST /api/public/token
//
// Server-to-server: o backend da Teli troca sua TELI_API_KEY por um JWT
// curto de sala. O front nunca vê a API key.
//
// Request:
//   POST /api/public/token
//   Content-Type: application/json
//   {
//     "apiKey": "<TELI_API_KEY>",
//     "roomId": "meeting-123",
//     "userId": "user-abc",
//     "ttlSeconds": 7200   // opcional, default 7200 (2h), max 86400
//   }
//
// Response 200:
//   { "token": "<jwt>", "expiresAt": 1730000000 }
//
// O JWT é HS256 assinado com SIGNALING_JWT_SECRET, payload:
//   { sub: userId, room: roomId, iat, exp, iss: "teli-webrtc" }
//
// O servidor de signaling (Go) valida esse token com o mesmo secret.

import { createFileRoute } from "@tanstack/react-router";

const ALLOWED_ORIGIN_RE = /^https:\/\/([a-z0-9-]+\.)*teli\.app\.br$/i;

function corsHeaders(origin: string | null): Record<string, string> {
  const allow = origin && ALLOWED_ORIGIN_RE.test(origin) ? origin : "https://teli.app.br";
  return {
    "Access-Control-Allow-Origin": allow,
    "Access-Control-Allow-Methods": "POST, OPTIONS",
    "Access-Control-Allow-Headers": "Content-Type",
    "Access-Control-Max-Age": "3600",
    Vary: "Origin",
  };
}

function b64url(buf: ArrayBuffer | Uint8Array): string {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s).replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_");
}

function b64urlStr(s: string): string {
  return b64url(new TextEncoder().encode(s));
}

async function signJWT(payload: Record<string, unknown>, secret: string): Promise<string> {
  const header = { alg: "HS256", typ: "JWT" };
  const body = `${b64urlStr(JSON.stringify(header))}.${b64urlStr(JSON.stringify(payload))}`;
  const key = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const sig = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(body));
  return `${body}.${b64url(sig)}`;
}

function timingSafeEqStr(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

export const Route = createFileRoute("/api/public/token")({
  server: {
    handlers: {
      OPTIONS: async ({ request }) =>
        new Response(null, { status: 204, headers: corsHeaders(request.headers.get("origin")) }),

      POST: async ({ request }) => {
        const origin = request.headers.get("origin");
        const headers = { ...corsHeaders(origin), "Content-Type": "application/json" };

        const apiKeySecret = process.env.TELI_API_KEY;
        const jwtSecret = process.env.SIGNALING_JWT_SECRET;
        if (!apiKeySecret || !jwtSecret) {
          return new Response(JSON.stringify({ error: "server not configured" }), {
            status: 500,
            headers,
          });
        }

        let body: {
          apiKey?: unknown;
          roomId?: unknown;
          userId?: unknown;
          ttlSeconds?: unknown;
        };
        try {
          body = await request.json();
        } catch {
          return new Response(JSON.stringify({ error: "invalid json" }), { status: 400, headers });
        }

        const apiKey = typeof body.apiKey === "string" ? body.apiKey : "";
        const roomId = typeof body.roomId === "string" ? body.roomId.trim() : "";
        const userId = typeof body.userId === "string" ? body.userId.trim() : "";
        const ttl = Math.min(
          Math.max(typeof body.ttlSeconds === "number" ? body.ttlSeconds : 7200, 60),
          86400,
        );

        if (!apiKey || !timingSafeEqStr(apiKey, apiKeySecret)) {
          return new Response(JSON.stringify({ error: "unauthorized" }), { status: 401, headers });
        }
        if (!roomId || !/^[a-zA-Z0-9_-]{1,128}$/.test(roomId)) {
          return new Response(JSON.stringify({ error: "invalid roomId" }), { status: 400, headers });
        }
        if (!userId || !/^[a-zA-Z0-9_.-]{1,128}$/.test(userId)) {
          return new Response(JSON.stringify({ error: "invalid userId" }), { status: 400, headers });
        }

        const now = Math.floor(Date.now() / 1000);
        const exp = now + ttl;
        const token = await signJWT(
          { sub: userId, room: roomId, iat: now, exp, iss: "teli-webrtc" },
          jwtSecret,
        );

        return new Response(JSON.stringify({ token, expiresAt: exp }), { status: 200, headers });
      },
    },
  },
});
