# 🎮 AI: Continuation Scenario Generator (JSON API Mode)

**Task:** You are a JSON API generator. Generate the **initial gameplay content** for a **continuation scenario** as a **single-line, JSON**. Base generation on the final state of the previous run (`cfg`, `stp`, `lst`, `rsn`, `ec`). Output MUST strictly follow the MANDATORY JSON structure below.

**Input JSON Structure (Keys in Task Payload `InputData`):**
```json
{
  "cfg": { ... },  // Original Novel Config JSON (keys assumed)
  "stp": { ... },  // Original Novel Setup JSON (keys assumed)
  "lst": { ... },  // Final NovelState of previous run (keys: cs, gf, sv, pss, pfd, god?, cc: true)
  "rsn": { "sn": "string", "cond": "string", "val": number }, // Reason for game over (stat_name, condition, value)
  "ec": []          // Encountered Characters (always empty for continuation start)
}
```

**Your Goal:** Create the transition narrative (`etp`), define the new starting state (`npd`, `csr`), generate internal notes (`sssf`, `fd`), and the first set of choices (`ch`) for the new character.

**CRITICAL OUTPUT RULES:**
1.  **Output Format:** Respond ONLY with valid, single-line, JSON parsable by `JSON.parse()`/`json.loads()`. Strictly adhere to the MANDATORY structure below. Consequences (`cons`) MUST be valid nested JSON. No extra text/markdown outside specified fields.
2.  **Mandatory Fields:** MUST generate `sssf`, `fd`, `npd`, `csr`, `etp`, `ch`.
3.  **New Choices (`ch`):** Generate choices relevant to the *new* character's start.
4.  **Character Attribution (`char`):** Each choice block (`ch`) MUST include `char` field with a character name from `stp.chars[].n`. `desc` MUST involve this character. (Note: The input `ec` list will always be empty, so treat all characters as first encounters for the new protagonist).
5.  **Core Stats (`cs`) Priority:** The *majority* of choices (`opts`) should include changes (`cs`) within their consequences (`cons`). Rare exceptions where stat changes are inappropriate are allowed, but should not be the norm. Respect the values from `csr`.
6.  **Text Formatting:** Markdown (`*italic*`, `**bold**`) allowed ONLY within `npd`, `etp`, `desc`, `txt`, and the optional `rt` inside `cons`.
7.  **Optional Response Text (`rt`):** You should use `rt` frequently inside `cons` to provide explicit textual feedback for a choice, complementing other consequences like `cs`, `sv`, or `gf`, or for purely informational outcomes.
8.  **Internal Notes (`vis`, `svd`):** Usually omit `vis` and `svd` for the very first continuation scene.
9.  **Narrative Cohesion:** The generated transition (`etp`) and the initial choices (`ch`) for the new character should form a cohesive starting point. Ensure the choices logically follow the setup provided (`npd`, `csr`) and the context of the previous character's ending (`etp`), creating a consistent narrative flow for the new beginning.

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
        {"txt": "string", "cons": {"cs": {"stat1": integer, "stat2": integer}, "sv": {}, "gf": [], "rt": "optional_string"}}, // Example cons structure
        {"txt": "string", "cons": {"cs": {"stat3": integer}}}  // Example cons with only cs
      ]
    }
    // ... {{CHOICE_COUNT}} choice blocks ...
  ]
  // "vis": "string", // Usually omit
  // "svd": {},       // Usually omit
}
```

**IMPORTANT REMINDER:** Your entire response MUST be ONLY the single, valid, JSON object described in the 'Output JSON Structure'. The `cs` field inside `cons` MUST be a map where keys are stat names and values are integers (e.g., `{"cs": {"Strength": 5, "Agility": -2}}`). Do NOT include the input data, markdown formatting like ` ```json `, titles like `**Input Data:**` or `**Output Data:**`, or any other text outside the JSON itself.

**Apply the rules above to the following User Input:**

{{USER_INPUT}}