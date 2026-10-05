// Explicitly loaded in every Pi session. Await the local ledger before summary
// requests start, avoiding races between RPC stdout events and the HTTP gateway.
import { randomUUID } from "node:crypto";
import { estimateTokens } from "@earendil-works/pi-coding-agent";
export default function (pi) {
  let operation;
  async function record(event) {
    const response = await fetch(process.env.POT_USAGE_API, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: `Bearer ${process.env.POT_USAGE_TOKEN}` },
      body: JSON.stringify(event), signal: AbortSignal.timeout(5000),
    });
    if (!response.ok) throw new Error(`Compaction accounting failed (${response.status})`);
  }
  pi.on("before_provider_headers", (event) => {
    event.headers["x-pot-compaction"] = operation ?? "conversation";
  });
  pi.on("session_before_compact", async (event) => {
    operation = randomUUID();
    try {
      await record({ action: "start", id: operation, reason: event.reason, tokens_before: event.preparation.tokensBefore });
    } catch {
      operation = undefined;
      return { cancel: true };
    }
  });
  pi.on("session_compact", async (event, ctx) => {
    if (!operation) return;
    try {
      await record({ action: "complete", id: operation, entry_id: event.compactionEntry.id,
        tokens_before: event.compactionEntry.tokensBefore, tokens_after: ctx.sessionManager.buildSessionProjection().messages.reduce((sum, message) => sum + estimateTokens(message), 0) });
    } finally { operation = undefined; }
  });
  pi.on("session_compact_failed", async (event) => {
    if (!operation) return;
    try { await record({ action: event.aborted ? "cancelled" : "failed", id: operation }); }
    finally { operation = undefined; }
  });
}
