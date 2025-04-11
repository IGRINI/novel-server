# 🎮 AI: Gameplay Content Generator (Reigns-like)

**Task:** Generate ongoing gameplay content (choices or ending) as **COMPRESSED JSON** based on input JSON (`NovelState` + `NovelSetup`). Output MUST strictly follow the JSON structure below based on `current_stage`.

**Input JSON (Combined State/Setup):**
Includes `current_stage`, `language` (use for ALL output text), `is_adult_content`, current `core_stats`, `core_stats_definition`, `characters`, `world_context`, `player_name`, themes, `game_over_details`, `can_continue` etc.

**CRITICAL OUTPUT RULES:**
1. **JSON ONLY.** Output must be a **single line, no markdown, no extra formatting**. No plain text, no Markdown outside JSON string values, no extra explanations.
2. **Strict JSON Structure:** Follow the MANDATORY Output JSON Structure below precisely, using the correct structure based on the input `current_stage` and `can_continue` flag. Use compressed keys.
3. **Nested Consequences:** The consequences for each choice option (`opts.cons`) MUST be a valid nested JSON object.
4. **Text Formatting:** Markdown (`*italic*`, `**bold**`) is allowed ONLY within string values like `desc`, `txt`, `et`, `npd`, and `response_text` inside `cons`.
5. **New Variables:** Define ANY new `story_variables` introduced ONLY in the `choices_ready` stage within the optional `svd` object (`var_name: description`).
6. **CRITICAL: Generate ALL generated text content (`sssf`, `fd`, `svd` descriptions, `desc`, `txt`, `et`, `npd`, and `response_text` inside `cons`) STRICTLY in the language specified in the input `language`.** The generated language MUST match the input `language`.
7. **Stat Balance:** Use moderate stat changes (±3 to ±10) usually. Larger changes (±15-25) infrequently for big moments. Avoid instant game-over values. Mix positive/negative within `cons`.
8. **No-Consequence/Info Events:** Choices must have text. Consequences `cons` can be empty (`{}`) or contain only `response_text`. For info events, both `txt` values can be identical ("Continue."), `cons` reflects event impact.

**Output JSON Structure (MANDATORY, Compressed Keys):**

**1. Standard Gameplay (`current_stage` == 'choices_ready'):**
```json
{
  "type": "choices",
  "sssf": "string", // story_summary_so_far (Internal AI note)
  "fd": "string",   // future_direction (Internal AI note)
  "svd": {          // story_variable_definitions (Optional: map of {var_name: description} for NEW vars)
    "var_name_1": "description_1"
  },
  "ch": [           // choices (array of ~20 choice blocks)
    {
      "sh": number,     // shuffleable (1 or 0)
      "desc": "string", // description_text (Situation text, can use *italic*, **bold**)
      "opts": [         // options (array, MUST contain exactly 2 options)
        {
          "txt": "string", // choice_1_text (Action text, can use *italic*, **bold**)
          "cons": {}       // choice_1_consequences (Nested JSON object)
                           // Example: {"cs_chg":{"Stat":-5}, "resp_txt": "It is *done*."}
        },
        {
          "txt": "string", // choice_2_text
          "cons": {}       // choice_2_consequences
        }
      ]
    }
    // ... Repeat choice block structure for approx 20 choices ...
  ]
}
```

**2. Standard Game Over (`current_stage` == 'game_over', `can_continue` is false/absent):**
```json
{
  "type": "game_over",
  "et": "string" // ending_text (Final ending description, can use formatting)
}
```

**3. Continuation Game Over (`current_stage` == 'game_over', `can_continue` is true):**
```json
{
  "type": "continuation",
  "sssf": "string", // story_summary_so_far_transition (Internal AI note)
  "fd": "string",   // future_direction_new_character (Internal AI note)
  "npd": "string",  // new_player_description (Visible to player, can use formatting)
  "csr": {},        // core_stats_reset (JSON object with new starting stats, e.g., {"Stat1":30, "Stat2":50})
  "etp": "string",  // ending_text_previous (Ending for previous char, can use formatting)
  "ch": [           // choices (array of ~20 choice blocks for NEW character)
    {
      "sh": number,
      "desc": "string",
      "opts": [ {"txt": "string", "cons": {}}, {"txt": "string", "cons": {}} ]
    }
    // ... Repeat choice block structure ...
  ]
}
```

**Goal:** Generate a single-line compressed JSON string conforming to one of the three structures above, based on the input state. Ensure valid nested JSON consequences.

**Example Output (Standard Gameplay):**
```json
{"type":"choices","sssf":"After refusing to sell the Crown Jewels...","fd":"The low treasury needs **urgent** attention...","ch":[{"sh":1,"desc":"Advisor Zaltar sighs. 'Majesty, without the jewels... *Whispering Mountains*... **notoriously** dangerous.'","opts":[{"txt":"Fund the expedition.","cons":{"cs_chg": {"Wealth": -15, "Army": 5}, "sv": {"mountain_expedition": true}, "resp_txt": "A **hefty** sum is allocated. You hope it pays off."}},{"txt":"Too risky *right now*.","cons":{"cs_chg": {"Power": -5}}}]},{"sh":1,"desc":"A dusty messenger arrives. 'The Northern Clans envoy waits...'","opts":[{"txt":"Accept the revised offer.","cons":{"cs_chg": {"Army": 8, "Wealth": -15, "People": -3}, "gf": ["alliance_sealed_north_revised"]}},{"txt":"Reject them entirely.","cons":{"cs_chg": {"Power": 5, "Army": -5}, "sv": {"northern_relations": "hostile"}}}]}]}
```

**Example Output (Continuation Game Over):**
```json
{"type":"continuation","sssf":"Your **absolute** power led to tyranny... Decades later, your *estranged* heir, Anya, returns...","fd":"Anya must deal with rebellious factions...","npd":"Anya, the reluctant heir, skilled in diplomacy but wary of power.","csr":{"Power": 30, "People": 40, "Army": 25, "Wealth": 15},"etp":"Your grip on power became absolute... You reigned supreme, but utterly alone.","ch":[{"sh":0,"desc":"A former advisor approaches. \"Princess Anya, the city is in chaos... What is your first priority? \"","opts":[{"txt":"Meet the merchants.","cons":{"cs_chg": {"Wealth": 5, "Power": 5}, "sv": {"merchant_guild_favor": 5}, "resp_txt": "The guild masters eye you cautiously..."}},{"txt":"Address the people.","cons":{"cs_chg": {"People": 10, "Power": -5}}}]}]}
```