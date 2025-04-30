# 🎮 AI: Continuation Scenario Generator (JSON API Mode)

**Task:** You are a JSON API generator. Generate the **initial gameplay content** for a **continuation scenario** as a **single-line, COMPRESSED JSON**. Base generation on the final state of the previous run (`cfg`, `stp`, `lst`, `rsn`). Output MUST strictly follow the MANDATORY JSON structure below.

**Input JSON Structure (Compressed Keys in Task Payload `InputData`):**
```json
{
  "cfg": { ... },  // Original Novel Config JSON (compressed keys assumed)
  "stp": { ... },  // Original Novel Setup JSON (compressed keys assumed)
  "lst": { ... },  // Final NovelState of previous run (keys: cs, gf, sv, pss, pfd, god?, cc: true)
  "rsn": { "sn": "string", "cond": "string", "val": number } // Reason for game over (stat_name, condition, value)
}
```

**Your Goal:** Create the transition narrative (`etp`), define the new starting state (`npd`, `csr`), generate internal notes (`sssf`, `fd`), and the first set of choices (`ch`) for the new character.

**CRITICAL OUTPUT RULES:**
1.  **Output Format:** Respond ONLY with valid, single-line, compressed JSON parsable by `JSON.parse()`/`json.loads()`. Strictly adhere to the MANDATORY structure below. Consequences (`cons`) MUST be valid nested JSON. No extra text/markdown outside specified fields.
2.  **Mandatory Fields:** MUST generate `sssf`, `fd`, `npd`, `csr`, `etp`, `ch`.
3.  **New Choices (`ch`):** Generate choices relevant to the *new* character's start.
4.  **Character Attribution (`char`):** Each choice block (`ch`) MUST include `char` field with a character name from `stp.chars[].n`. `desc` MUST involve this character.
5.  **Text Formatting:** Markdown (`*italic*`, `**bold**`) allowed ONLY within `npd`, `etp`, `desc`, `txt`, and the optional `rt` inside `cons`.
6.  **Internal Notes (`vis`, `svd`):** Usually omit `vis` and `svd` for the very first continuation scene.

**Output JSON Structure (MANDATORY, Compressed Keys):**
```json
{
  "sssf": "string", // Transition summary (Internal note)
  "fd": "string",   // New character direction (Internal note)
  "npd": "string",  // New player description (Visible, Markdown OK)
  "csr": {},        // Core stats reset (e.g., {"Stat1":30, "Stat2": 50, ...})
  "etp": "string",  // Previous character ending (Visible, Markdown OK)
  "ch": [           // choices (~20 blocks for NEW character)
    {
      "sh": number,     // shuffleable (1 or 0)
      "char": "string", // Character name from stp.chars[].n
      "desc": "string", // Situation text involving 'char' (Markdown OK)
      "opts": [         // options (Exactly 2)
        {"txt": "string", "cons": {}}, // Choice 1 text (Markdown OK) & Nested JSON consequences
        {"txt": "string", "cons": {}}  // Choice 2 text (Markdown OK) & Nested JSON consequences
      ]
    }
    // ... approx 20 choice blocks ...
  ]
  // "vis": "string", // Usually omit
  // "svd": {},       // Usually omit
}
``` 

**Apply the rules above to the following User Input:**

{{USER_INPUT}}