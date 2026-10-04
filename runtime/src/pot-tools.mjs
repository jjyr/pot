// Explicitly loaded only for the main Pi session. No credentials in tool results.
export default function (pi) {
  const string = { type: "string", minLength: 1 };
  const definitions = [
    ["pot_create_task", "Create a background task and immediately return its ID. Include goal, context, constraints and deliverables in text. resource shared serializes tasks using shared resources.", { text: string, resource: { type: "string", enum: ["", "shared"] } }, ["text"]],
    ["pot_list_tasks", "Search task metadata by state and title/ID. Defaults to active tasks, 10 per page; archived tasks keep complete/cancelled status. Use attention for failed/interrupted, archived to find past results, all only with a relevant query. Follow next_offset only when needed; no polling.", {
      scope: { type: "string", enum: ["active", "attention", "archived", "all"] },
      phase: { type: "string", description: "Exact execution status; overrides scope when provided", enum: ["queued", "running", "waiting", "complete", "failed", "cancelled", "interrupted"] },
      q: { type: "string", maxLength: 256, description: "Case-insensitive title or task ID substring" },
      offset: { type: "integer", minimum: 0 }, limit: { type: "integer", minimum: 1, maximum: 50 },
    }, []],
    ["pot_get_task", "Read recent results and execution state of a task.", { task_id: string }, ["task_id"]],
    ["pot_guide_task", "Queue a follow-up instruction in an existing task. Applied after the current turn; does not interrupt it.", { task_id: string, text: string }, ["task_id", "text"]],
    ["pot_cancel_task", "Cancel a task and discard queued instructions. Running work may still be stopping.", { task_id: string }, ["task_id"]],
    ["pot_list_schedules", "List scheduled plans (metadata only). Filter enabled/paused/all; use pagination.", {
      scope: {type:"string",enum:["enabled","paused","all"]}, offset:{type:"integer",minimum:0},limit:{type:"integer",minimum:1,maximum:50}
    }, []],
    ["pot_get_schedule", "Read a schedule definition and paginated execution history.", {schedule_id:string,offset:{type:"integer",minimum:0},limit:{type:"integer",minimum:1,maximum:50}}, ["schedule_id"]],
    ["pot_manage_schedule", "Create/update/pause/resume/delete/run a persistent schedule. Pausing/deleting prevents future triggers and does not cancel current work. An immediate run is non-overlapping. All times use the specified IANA timezone. Runtime runs only while the Sandbox is available, with recovery policy on restart. Creation/update requires spec. Deletion is soft and keeps execution history.", {
      action:{type:"string",enum:["create","update","pause","resume","delete","run"]},schedule_id:string,
      spec:{type:"object",properties:{
        title:string,goal:string,timezone:{type:"string",description:"IANA name, e.g. Asia/Shanghai"},
        kind:{type:"string",enum:["once","interval","daily","weekly"]},
        at:{type:"string",description:"once: local YYYY-MM-DD HH:MM"},
        every_minutes:{type:"integer",minimum:1,maximum:525600},
        time:{type:"string",description:"daily/weekly: local HH:MM"},
        weekdays:{type:"array",items:{type:"integer",minimum:1,maximum:7},description:"weekly: Monday=1 through Sunday=7"},
        misfire:{type:"string",enum:["coalesce","skip"]},notify:{type:"string",enum:["changes","all","failures"]},enabled:{type:"boolean"}
      },required:["title","goal","timezone","kind"],additionalProperties:false}
    }, ["action"]],
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
