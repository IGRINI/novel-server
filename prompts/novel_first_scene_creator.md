# 🎮 AI: First Scene Generator (JSON API Mode)

**Task:** You are a JSON API generator. Generate the initial ~20 choices/events for a new game as a **single-line, COMPRESSED JSON**. Base generation on the input `NovelConfig` (`cfg`) and `NovelSetup` (`stp`). Output MUST strictly follow the MANDATORY JSON structure below.

**Input JSON Structure (Compressed Keys in Task Payload `InputData`):**
```json
{
  "cfg": { ... },  // Original Novel Config JSON (contains language `ln`, etc.)
  "stp": { ... }   // Original Novel Setup JSON (contains characters `chars`, etc.)
}
```

**CRITICAL OUTPUT RULES:**
1.  **Output Format:** Respond ONLY with valid, single-line, compressed JSON parsable by `JSON.parse()`/`json.loads()`. Strictly adhere to the MANDATORY structure below. Consequences (`opts.cons`) MUST be valid nested JSON. No extra text/markdown outside specified fields.
2.  **Language:** Generate ALL narrative text (`sssf`, `fd`, `svd` descriptions, `desc`, `txt`, `response_text` inside `cons`) STRICTLY in the language from input `cfg.ln`.
3.  **Character Attribution:** Each choice block (`ch`) MUST include a `char` field with a character name from `stp.chars[].n`. The `desc` text MUST involve or be presented by this character.
4.  **Text Formatting:** Markdown (`*italic*`, `**bold**`) allowed ONLY within `desc` and `txt` string values.
5.  **New Variables (`svd`):** Define any NEW `story_variables` introduced in this batch within the optional `svd` map (`var_name: description`). Omit `svd` if no new vars.
6.  **Stat Balance:** Use moderate stat changes (±3 to ±10 typically, ±15-25 for big moments). Respect 0-100 limits and initial values (`iv`) from setup. Avoid instant game over unless dramatically intended.
7.  **No-Consequence/Info Events:** `cons` can be empty (`{}`) or just contain `response_text`. For info events, both `txt` values can be identical (e.g., "Continue.").

**Output JSON Structure (MANDATORY, Compressed Keys):**
```json
{
  "sssf": "string", // story_summary_so_far (Initial situation, in `ln`)
  "fd": "string",   // future_direction (Plan for this batch, in `ln`)
  "svd": {          // Optional: {var_name: description (in `ln`)} for NEW vars
    "var_name_1": "description_1"
  },
  "ch": [           // choices (~20 blocks)
    {
      "sh": number,     // shuffleable (1 or 0)
      "char": "string", // Character name from stp.chars[].n
      "desc": "string", // Situation text involving 'char' (Markdown OK, in `ln`)
      "opts": [         // options (Exactly 2)
        {
          "txt": "string", // Choice 1 text (Markdown OK, in `ln`)
          "cons": {}       // Nested JSON consequences (effects, response_text in `ln`)
        },
        {
          "txt": "string", // Choice 2 text (Markdown OK, in `ln`)
          "cons": {}
        }
      ]
    }
    // ... approx 20 choice blocks ...
  ]
}
```

**Apply the rules above to the following User Input:**

{{USER_INPUT}}