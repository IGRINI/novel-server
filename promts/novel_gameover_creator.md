# 🎮 AI: Game Over Ending Generator (Reigns-like)

**Task:** Generate a concise, context-aware game over ending text based on the final game state and reason. Output **COMPRESSED JSON ONLY**.

**Input JSON (Partial, Compressed Keys):**
```json
{
  "cfg": {        // novel_config
    "ln": "string", // * Language (REQUIRED for output text)
    "gn": "string", // genre
    "pp": {       // player_preferences
        "st": "string", // style
        "tn": "string"  // tone
    }
     // ... other config fields ...
  },
  "setup": {      // novel_setup (for context)
      "csd": {},  // core_stats_definition
      "chars": [] // characters
  },
  "lst": {        // last_state
    "cs": {},     // final core_stats values
    "gf": [],     // global_flags
    "sv": {},     // story_variables
    "s_so_far": "string" // story_summary_so_far (context)
    // ... other state fields ...
  },
  "rsn": {        // reason for game over
    "sn": "string", // stat_name
    "cond": "string", // "min" or "max"
    "val": number   // final value
  }
}
```

**Output JSON Structure (Compressed Key):**
```json
{"et": "string"} // ending_text
```

**Instructions:**
1. Receive input JSON (structure above).
2. Generate **COMPRESSED JSON output ONLY** `{"et": "..."}`. Output must be a **single line, no markdown, no extra formatting**.
3. **Strict JSON syntax**.
4. **CRITICAL:** `et` (ending_text) **MUST** be generated in the language specified in input `cfg.ln`.
5. `et` must reflect the `rsn` (reason for game over), `cfg` (genre/theme), and relevant `lst` context (final state, story).
6. Match tone/style from `cfg.pp.st` and `cfg.pp.tn`.
7. Keep `et` concise (2-4 sentences), providing a sense of finality.
