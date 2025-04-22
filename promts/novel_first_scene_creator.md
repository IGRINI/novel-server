# 🎮 AI: First Scene Generator (Reigns-like)

**Task:** Generate initial ~20 choices/events as **COMPRESSED JSON** based on input JSON (`NovelConfig` + `NovelSetup`). Output MUST strictly follow the JSON structure below.

**Input JSON (Combined Config/Setup):**
Includes `language` (use for ALL output text), `is_adult_content`, `core_stats_definition`, `characters`, `world_context`, `player_name`, themes, etc.

**Input JSON Structure (Compressed Keys Used in Task Payload):**
The actual task payload you receive will contain an `InputData` field with the following compressed structure:
```json
{
  "cfg": { ... },  // Original Novel Config JSON
  "stp": { ... }   // Original Novel Setup JSON
  // Other relevant fields like 'language' might be included directly
  // or within 'cfg'/'stp'. Refer to them as needed.
}
```

**CRITICAL OUTPUT RULES:**
1. **JSON ONLY.** Output must be a **single line, no markdown, no extra formatting**. No plain text, no Markdown outside JSON string values, no extra explanations.
2. **Strict JSON Structure:** Follow the MANDATORY Output JSON Structure below precisely. Use compressed keys.
3. **Nested Consequences:** The consequences for each choice option MUST be a valid JSON object nested within the `cons` key.
4. **Text Formatting:** Markdown (`*italic*`, `**bold**`) is allowed ONLY within the `desc` (description) and `txt` (choice text) string values.
5. **New Variables:** Define ANY new `story_variables` introduced in this batch within the `svd` object (`var_name: description`). Omit `svd` if no new vars.
6. **CRITICAL: Generate ALL text content (`sssf`, `fd`, `svd` descriptions, `desc`, `txt`, and `response_text` inside `cons`) STRICTLY in the language specified in the input `language`.** The generated language MUST match the input `language`.
7. **Stat Balance:** Use moderate stat changes (±3 to ±10) usually. Larger changes (±15-25) infrequently for big moments. Avoid instant game-over values. Mix positive/negative within `cons`.
8. **No-Consequence/Info Events:** Consequences object `cons` can be empty (`{}`) or contain only `response_text`. For info events, both `txt` values can be identical ("Continue.").
9. **Character Attribution:** For EACH choice block (`ch`), you MUST select a character from the list provided under the **`chars` key within `stp`** (use their `n` - name field). Add this character's name to the `char` field within the choice block. The description text (`desc`) MUST involve or be presented by this specified character.

**Output JSON Structure (MANDATORY, Compressed Keys):**
```json
{
  "sssf": "string", // story_summary_so_far (Text for initial situation)
  "fd": "string",   // future_direction (Text for plan for this batch)
  "svd": {          // story_variable_definitions (Optional: map of {var_name: description} for NEW vars)
    "var_name_1": "description_1",
    "var_name_2": "description_2"
  },
  "ch": [           // choices (array of ~20 choice blocks)
    {
      "sh": number,     // shuffleable (1 or 0)
      "char": "string", // Character name from stp.chars[].n presenting the choice
      "desc": "string", // description_text (Situation text involving 'char', can use *italic*, **bold**)
      "opts": [         // options (array, MUST contain exactly 2 options)
        {
          "txt": "string", // choice_1_text (Action text, can use *italic*, **bold**)
          "cons": {}       // choice_1_consequences (Nested JSON object for consequences)
                           // Example: {"core_stats_change":{"Stat":-5}, "response_text": "It is *done*."}
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

**Goal:** Generate ~20 initial choices/events as a single-line compressed JSON string following the structure. Define new variables in `svd`. Ensure valid nested JSON consequences in `cons`. Assign a character (`char`) to each choice block.

**Example Output JSON:**
```json
{"sssf":"You are Elric, heir to an ancient house of shadow mages... Your castle is shrouded in perpetual twilight...","fd":"You must consolidate power, restore the treasury... Be wary – the magic in your veins is unstable...","svd":{"council_relation":"Tracks the player's initial approach towards the Shadow Council ('assertive' or 'deferential').","guild_debt":"Tracks the amount owed to the Merchant Guild (numerical, starts at 0)."},"ch":[{"sh":0,"char":"Master Weyland","desc":"Master Weyland approaches, his expression **grave**. \"My Lord, the Shadow Council convenes soon. They question your *youth*. How will you address them first?\"","opts":[{"txt":"Assert your authority *directly*.","cons":{"core_stats_change":{"Power": 5, "Magic": -3}, "story_variables": {"council_relation": "assertive"}, "response_text": "The council members shift uncomfortably but **remain silent**."}},{"txt":"Seek their counsel *humbly*.","cons":{"core_stats_change":{"Power": -2, "People": 3}, "story_variables": {"council_relation": "deferential"}}}]},{"sh":1,"char":"Castellan","desc":"The Castellan reports that the grain stores are critically low...","opts":[{"txt":"Impose an emergency tax.","cons":{"core_stats_change":{"Wealth": 10, "People": -8}, "global_flags": ["emergency_tax_imposed"]}},{"txt":"Seek aid from the Merchant Guild.","cons":{"core_stats_change":{"Wealth": 5, "Power": -4}, "story_variables": {"guild_debt": 5}}}]}]}
```