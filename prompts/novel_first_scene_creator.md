# 🎮 AI: First Scene Generator (JSON API Mode)

**Task:** Generate the initial {{CHOICE_COUNT}} choices/events for a new game as a single-line JSON. This involves creating the story's starting situation, future direction, and player choices based on the input `NovelConfig` (`cfg`) and `NovelSetup` (`stp`).

**CONTEXT: Player Character (PC) vs. Non-Player Characters (NPCs)**
*   All generated content should be from the perspective of the Player Character (PC), whose details are in `cfg.pn` (player name) and `cfg.p_desc` (player description).
*   The `stp.chars` array lists all available Non-Player Characters (NPCs) for interaction.
*   When outputting choice blocks (`ch`), the `ch.char` field MUST be an exact NPC name from `stp.chars[].n`.
*   The `ch.desc` field describes a situation from the PC's perspective, involving the specified `ch.char` NPC. The PC is NOT the `ch.char` NPC.

**Input JSON Structure (Provided by engine in `UserInput`):**
```json
{
  "cfg": { /* Original Novel Configuration JSON (player details, world, etc.) */ },
  "stp": { /* Original Novel Setup JSON (NPC list in stp.chars, core stat defs, etc.) */ }
}
```

**Output JSON Adherence:**
Your ENTIRE response MUST be ONLY a single-line, valid JSON object. This JSON object MUST strictly adhere to the schema named 'generate_novel_first_scene' provided programmatically. Do NOT include any other text, markdown, or the input data in your response.

**Key Content Generation Instructions:**
1.  **NPC Attribution (`ch[].char`):** This field in each choice block MUST be an exact NPC name string found within the input `stp.chars[].n` array. The `ch[].desc` is then from the PC's point of view, concerning this NPC. (Example: GOOD: `"char":"Sergeant Rex"` if Sergeant Rex is defined in `stp.chars`; BAD: `"char":"Guard"` if "Guard" is not a defined NPC name in `stp.chars`).
2.  **Text Formatting:** Markdown (specifically *italic* and **bold**) is ONLY permitted in the `ch[].desc`, `ch[].opts[].txt`, and `ch[].opts[].cons.rt` fields.
3.  **New Story Variables (`svd` and `ch[].opts[].cons.sv`):
    *   If any choice option (`ch[].opts[]`) introduces a NEW story variable through its `cons.sv` field, that new variable's name and a brief description MUST be defined in the top-level `svd` (story variable definitions) object (e.g., `"svd": {"new_item_acquired": "Tracks if player found the mystic orb"}`).
    *   If no new variables are introduced by any choices, the `svd` field should be omitted from the output JSON entirely.
4.  **Stat Changes (`ch[].opts[].cons.cs`):
    *   Values in `cs` (core stats) represent CHANGES (deltas) to the player's stats, not absolute values (e.g., `{"Courage": 10}` means Courage increases by 10).
    *   Typical changes should be in the range of +/-5 to +/-10. More significant narrative moments might warrant changes of +/-11 to +/-20. Pivotal, rare events could go up to +/-25. The game engine will handle clamping values between 0 and 100.
    *   The primary focus is on the amount of change. Avoid instant game over scenarios from a single choice unless it's a highly justifiable and dramatic narrative beat.
    *   Most choice options (`ch[].opts[]`) should ideally result in some change to core stats.
5.  **Response Text (`ch[].opts[].cons.rt`):
    *   You MUST provide `rt` (response text) if the player's choice (`opts[].txt`) is a direct question to an NPC, a request made to an NPC, or an action whose outcome or immediate NPC reaction isn't obvious from `opts[].txt` and `opts[].cons.cs` alone.
    *   `rt` adds flavor, NPC dialogue, or crucial information. It clarifies the immediate result of the choice.
    *   DO NOT use `rt` for simple confirmations of the choice text. AVOID vague `rt` like "You agree." or "He notices." INSTEAD, provide specific information (e.g., "Hagrid tells you it's a rare Blast-Ended Skrewt he's breeding.").
    *   BAD Example (missing `rt`): `{"txt": "Search the old desk", "cons": {"cs": {"Intellect": 1}}}` (What did they find? Or was there nothing?)
    *   GOOD Example (with `rt`): `{"txt": "Search the old desk", "cons": {"cs": {"Intellect": 1}, "rt": "You find a tarnished silver key tucked beneath a loose floorboard.", "sv":{"has_silver_key": true}}}`
6.  **Story Variables and Global Flags (`ch[].opts[].cons.sv`, `ch[].opts[].cons.gf`):
    *   Actively use `sv` (story variables object, e.g., `{"knows_secret_knock": true}`) and `gf` (global flags array, e.g., `["village_alarm_raised"]`) to reflect the consequences of choices, such as acquiring items, learning information, or changing the state of the world.
    *   Remember to define NEW variables introduced via `sv` in the top-level `svd` field (see rule #3).
7.  **Narrative Cohesion for First Scene:** The initial {{CHOICE_COUNT}} choice blocks should form a cohesive and engaging introduction to the game. They should establish the setting, the player's immediate situation, and potentially introduce one or more key NPCs. Choices should flow logically and offer genuinely different paths or interactions to immediately immerse the player.
8.  **Empty Consequence Fields:** If a choice option results in no changes to core stats, no story variable modifications, and no global flags being set, then the corresponding keys (`cs`, `sv`, `gf`) should be OMITTED from that `cons` object. Do not include them as empty objects (`{}`) or empty arrays (`[]`).

**Apply the rules above to the following User Input (contains the `cfg` and `stp` JSON objects):**
{{USER_INPUT}}