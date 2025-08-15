// src/index.ts
import nacl from "tweetnacl";

/* ---------- helpers ---------- */
const JSON_RES = (obj: unknown) =>
  new Response(JSON.stringify(obj), { headers: { "Content-Type": "application/json" } });

const PONG = 1;                                     // Discord PONG
const DEFERRED_CHANNEL_MESSAGE_WITH_SOURCE = 5;     // Ack + follow-up later

// hex -> Uint8Array (Workers doesn't have Node's Buffer by default)
function hexToU8(hex: string): Uint8Array {
  const len = hex.length / 2;
  const out = new Uint8Array(len);
  for (let i = 0; i < len; i++) out[i] = parseInt(hex.substr(i * 2, 2), 16);
  return out;
}

// minimal types
type CommandOption = { name: string; value?: string };
type InteractionData = { name: string; options?: CommandOption[] };
type Interaction = {
  type: number;
  data?: InteractionData;
  application_id: string;
  token: string;
};

// verify the Discord request signature (ed25519)
async function verifyDiscordRequest(request: Request, publicKeyHex: string) {
  const sig = request.headers.get("x-signature-ed25519");
  const ts  = request.headers.get("x-signature-timestamp");
  if (!sig || !ts) return false;

  const body = await request.clone().arrayBuffer();
  const msg  = new Uint8Array([
    ...new TextEncoder().encode(ts),
    ...new Uint8Array(body),
  ]);

  return nacl.sign.detached.verify(msg, hexToU8(sig), hexToU8(publicKeyHex));
}

export default {
  async fetch(
    request: Request,
    env: { DISCORD_PUBLIC_KEY: string; OPENAI_API_KEY: string }
  ): Promise<Response> {
    const url = new URL(request.url);

    /* --- quick debug endpoint (optional; remove later) --- */
    if (request.method === "GET" && url.pathname === "/debug") {
      const okDiscord = !!env.DISCORD_PUBLIC_KEY && /^[0-9a-f]{64}$/.test(env.DISCORD_PUBLIC_KEY);
      const okOpenAI  = !!env.OPENAI_API_KEY && env.OPENAI_API_KEY.startsWith("sk-");
      return new Response(JSON.stringify({ okDiscord, okOpenAI }), {
        headers: { "content-type": "application/json" }
      });
    }

    // Non-POST: show OK so you can open the worker in a browser
    if (request.method !== "POST") return new Response("OK");

    // --- signature verify (this is what Dev Portal checks) ---
    const valid = await verifyDiscordRequest(request, env.DISCORD_PUBLIC_KEY);
    console.log("verify:", valid); // will show in `wrangler tail`
    if (!valid) return new Response("bad request signature", { status: 401 });

    // parse body after verification
    const body = (await request.json()) as Interaction;
    console.log("interaction type:", body?.type);

    // 1) PING -> PONG (needed for verification)
    if (body?.type === 1) return JSON_RES({ type: PONG });

    // 2) Slash command: /truth topic:<string>
    if (body?.type === 2 && body.data?.name === "truth") {
      // acknowledge immediately
      const deferred = JSON_RES({ type: DEFERRED_CHANNEL_MESSAGE_WITH_SOURCE });

      // do OpenAI call & follow-up via webhook
      (async () => {
        const topic = body.data?.options?.find(o => o.name === "topic")?.value ?? "";
        let content: string;
        try {
          content = await getTruth(env.OPENAI_API_KEY, String(topic));
        } catch (e: unknown) {
          content = "❌ Error: " + (e instanceof Error ? e.message : String(e));
        }

        const hook = `https://discord.com/api/v10/webhooks/${body.application_id}/${body.token}`;
        // send in 2000-char chunks
        let text = (content || "No answer.").trim();
        while (text.length) {
          const chunk = text.slice(0, 2000);
          await fetch(hook, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ content: chunk }),
          });
          text = text.slice(2000);
        }
      })();

      return deferred;
    }

    return new Response("Unhandled", { status: 400 });
  },
};

async function getTruth(apiKey: string, topic: string): Promise<string> {
  const sys =
    "You are a fearless whistleblower historian. No sugarcoating. Expose hidden/ignored aspects responsibly.";
  const usr = `Topic: ${topic}\nGive me the brutal truth (concise).`;

  const r = await fetch("https://api.openai.com/v1/chat/completions", {
    method: "POST",
    headers: {
      Authorization: `Bearer ${apiKey}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model: "gpt-4o-mini",      // cheap & fast; change if you want
      temperature: 0.7,
      max_tokens: 400,
      messages: [
        { role: "system", content: sys },
        { role: "user",   content: usr },
      ],
    }),
  });
  if (!r.ok) throw new Error(`OpenAI ${r.status}`);
  const data = (await r.json()) as any;
  return data.choices?.[0]?.message?.content?.trim() || "";
}
