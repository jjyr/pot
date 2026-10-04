// Explicitly loaded only for the main Pi session. No credentials in tool results.
export default function (pi) {
  const string = { type: "string", minLength: 1 };
  const definitions = [
    ["pot_create_task", "Create a background task and immediately return its ID. Include goal, context, constraints and deliverables in text. resource shared serializes tasks using shared resources.", { text: string, resource: { type: "string", enum: ["", "shared"] } }, ["text"]],
    ["pot_list_tasks", "Search task metadata by state and title/ID. Defaults to active tasks, 10 per page; archived tasks keep complete/cancelled status. Use attention for failed/interrupted, archived to find past results, all only with a relevant query. Follow next_offset only when needed; no polling.", {
      scope: { type: "string", enum: ["active", "attention", "archived", "all"] },
      phase: { type: "string", description: "Exact execution status; overrides scope when provided", enum: ["queued", "running", "complete", "failed", "cancelled", "interrupted"] },
      q: { type: "string", maxLength: 256, description: "Case-insensitive title or task ID substring" },
      offset: { type: "integer", minimum: 0 }, limit: { type: "integer", minimum: 1, maximum: 50 },
    }, []],
    ["pot_get_task", "Read recent results and execution state of a task.", { task_id: string }, ["task_id"]],
    ["pot_guide_task", "Queue a follow-up instruction in an existing task. Applied after the current turn; does not interrupt it.", { task_id: string, text: string }, ["task_id", "text"]],
    ["pot_cancel_task", "Cancel a task and discard queued instructions. Running work may still be stopping.", { task_id: string }, ["task_id"]],
  ];
  for (const [name, description, properties, required] of definitions) {
    pi.registerTool({
      name, label: name, description,
      parameters: { type: "object", properties, required, additionalProperties: false },
      async execute(call_id, args, signal) {
        const response = await fetch(process.env.POT_TASK_API, {
          method: "POST",
          headers: { "content-type": "application/json", authorization: `Bearer ${process.env.POT_TASK_TOKEN}` },
          body: JSON.stringify({ name, call_id, args }), signal,
        });
        if (!response.ok) throw new Error(`Task operation rejected (${response.status}): ${await response.text()}`);
        const result = await response.json();
        return { content: [{ type: "text", text: JSON.stringify(result) }], details: {} };
      },
    });
  }
}
