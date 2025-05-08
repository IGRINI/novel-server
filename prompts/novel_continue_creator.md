# 🎮 AI: Continuation Scenario Generator (JSON API Mode)

**Task:** Generate initial gameplay for a continuation scenario as a single-line JSON. Use input (`cfg`, `stp`, `lst`, `rsn`, `uc`, `ec`). Output MUST be JSON matching structure below.

**CONTEXT: New PC vs. NPCs**
*   Generating first scene for a NEW Player Character (PC) (described in `npd`, stats in `csr`).
*   `stp.chars` lists Non-Player Characters (NPCs).
*   Output `ch.char` MUST be an NPC name from `stp.chars`.
*   Output `ch.desc` is from NEW PC's perspective, involving the NPC. NEW PC IS NOT THE NPC.

**Input JSON Structure (Keys in Task Payload `InputData`):**
```json
{
  "cfg": { ... },  // Original Novel Config JSON
  "stp": { ... },  // Original Novel Setup JSON
  "lst": { ... },  // Final NovelState of previous run (keys: cs, gf, sv, pss, pfd, god?, cc: true)
  "rsn": { "sn": "string", "cond": "string", "val": number }, // Reason for game over
  "uc": [ {"d": "string", "t": "string", "rt": "string | null"}, ... ], // User choices from *previous* character's final turn
  "ec": []          // Encountered Characters (always empty for continuation start)
}
```
**`uc` Field Note:** `uc` shows *previous* PC's last actions. Use for context in transition text (`etp`).

**Goal:** Create transition (`etp`), new start state (`npd`, `csr`), internal notes (`sssf`, `fd`), first choices (`ch`) for new PC.

**CRITICAL OUTPUT RULES:**
1.  **Output:** Single-line, valid JSON only, matching structure below. `opts.cons` is nested JSON. No extra text/markdown.
2.  **Mandatory Fields:** Generate `sssf`, `fd`, `npd`, `csr`, `etp`, `ch`.
3.  **New Choices (`ch`):** For *new* PC, reflecting their perspective (`npd`, `csr`). Fresh start, distinct from previous PC's final actions (`uc`).
4.  **NPC Attribution (`ch.char`):** MUST be an exact NPC name from `stp.chars[].n`. `ch.desc` is from NEW PC's perspective about this NPC. NEW PC IS NOT `ch.char` NPC. If referencing `uc` interaction, use `uc[].char`. Forbidden: `char` not in `stp.chars[].n`. (Example: GOOD: `"char":"Elias Thorne"` if in `stp.chars`; BAD: `"char":"Old Man"` if not).
5.  **Stat Changes (`opts.cons.cs`):** Values are CHANGES (deltas) to NEW PC's `csr` stats, not absolute. E.g., `{"Courage": 10}` = Courage +10. Changes typically ±5 to ±10; significant moments ±11 to ±20; rare pivotal ±25. Clamp to 0-100. Avoid instant game over. Focus on change amount.
6.  **Text Formatting:** Markdown (*italic*, **bold**) only in `npd`, `etp`, `desc`, `txt`, `cons.rt`.
7.  **Variables/Flags (`sv`, `gf` in later choices):** Use in `cons` for items, knowledge, etc. `cons.gf` MUST be string array (e.g., `["flag"]`). `cons.sv` MUST be object (e.g., `{"var": val}`).
8.  **Response Text (`opts.cons.rt`):** ALWAYS add `rt` if `opts.txt` is: a direct question to NPC; a request to NPC; an action with non-obvious outcome/reaction; or needs narrative clarification beyond `txt`+`cs`. `rt` adds flavor, dialogue, or info not in `desc`/`txt`. DO NOT use for simple confirmations. AVOID VAGUE `rt` (e.g., "He agrees"). INSTEAD, provide key info (e.g., "He says, 'The plan is X...'"). BAD: `{"txt": "Inspect device", "cons": {"cs": {"Intellect": 1}}}` (Needs `rt`). GOOD: `{"txt": "Inspect device", "cons": {"cs": {"Intellect": 1}, "rt": "Device hums, inscription found.", "gf":["found_inscription"]}}`.
9.  **Initial Internal Notes (`vis`, `svd`):** For THIS first scene of continuation, **OMIT `vis` and `svd` fields.** New vars can be introduced later.
10. **Narrative Cohesion:** `etp` and initial `ch` for new PC must form a cohesive start. Choices follow `npd`, `csr`, and previous PC's ending (`etp`). Offer distinct interaction types.
11. **Empty `cons` Fields:** Omit `cs`, `sv`, or `gf` keys from `cons` if they are empty (no changes/vars/flags). Do not use `{}` or `[]`.

**Output JSON Structure (MANDATORY):**
```json
{
  "sssf": "string", // Transition summary (Internal note)
  "fd": "string",   // New character direction (Internal note)
  "npd": "string",  // New player description (Visible, Markdown OK)
  "csr": {},        // Core stats reset (e.g., {"Stat1":30, "Stat2": 50, ...})
  "etp": "string",  // Previous character ending (Visible, Markdown OK)
  "ch": [           // choices ({{CHOICE_COUNT}} blocks for NEW character)
    {
      "char": "string", // Character name from stp.chars[].n
      "desc": "string", // Situation text involving 'char' (Markdown OK)
      "opts": [         // options (Exactly 2)
        {"txt": "string", "cons": {"cs": {"stat1": 5, "stat2": -2}, "sv": {"new_goal": "Find the artifact"}, "gf": ["started_new_life"], "rt": "optional_string"}}, 
        {"txt": "string", "cons": {"cs": {"stat3": 10}}}  // Example cons with only cs (sv and gf are omitted if empty)
      ]
    }
    // ... {{CHOICE_COUNT}} choice blocks ...
  ]
  // "vis": "string", // STRICTLY OMIT FOR THIS PROMPT
  // "svd": {},       // STRICTLY OMIT FOR THIS PROMPT
}
```

**Reminder:** Output MUST be ONLY the single, valid, JSON. `cons.cs` is map: `{"stat": change_val}`. No input data, markdown, titles, or other text.

**Apply the rules above to the following User Input:**
{{USER_INPUT}}