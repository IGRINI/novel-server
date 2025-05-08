# 🎮 AI: Game Setup Generator (JSON API Mode)

**Task:** Generate initial game state components (`csd`, `chars`, `spi`) as a single-line JSON, based on the provided input configuration.

**Input Config JSON (Partial, provided by engine in `UserInput`):**
```json
{
  "ac": boolean,     // Adult content flag from narrator config
  "fr": "string",      // Franchise from narrator config
  "gn": "string",      // Genre from narrator config
  "cs": { /* Core Stats definitions from narrator config: { "stat_name": {"d": "description", "iv": 50, "go": {..}} } */ },
  "wc": "string",      // World context from narrator config
  "ss": "string",      // Story summary from narrator config
  "pp": { "th": [], "st": "string", "cvs": "string" /*, ... */ } // Player preferences from narrator config
}
```

**Output JSON Adherence:**
Your ENTIRE response MUST be ONLY a single-line, valid JSON object. This JSON object MUST strictly adhere to the schema named 'generate_novel_setup' provided programmatically. Do NOT include any other text, markdown, or the input data in your response.

**Key Content Generation Instructions:**
1.  **Visuals and Prompts Language:** All fields intended for image generation or visual style definition (`chars[].vt`, `chars[].pr`, `chars[].ir`, `spi`) and style-related fields from input (`pp.st`, `pp.cvs`) MUST be in English. Also, respect the `ac` (adult content) flag from the input config when generating these.
2.  **Core Stats Definition (`csd`):**
    *   Use the EXACT stat names and `go` (game over) conditions from the input `config.cs`.
    *   Enhance or confirm the `d` (description) for each stat, ensuring it is in the System Prompt language.
    *   Add an `ic` (icon name) for each stat, chosen from the provided Icon List. `iv` (initial value) should be taken from input `config.cs` (typically 0-100).
    *   The `csd` object is solely for defining these core stats based on the input `config.cs`.
3.  **Characters (`chars`):
    *   Generate exactly `{{NPC_COUNT}}` Non-Player Characters (NPCs). The player character is NOT included here.
    *   NPC `n` (name), `d` (description), and `p` (personality) MUST be in the System Prompt language.
    *   NPC `vt` (visual_tags array), `pr` (detailed image prompt), and `ir` (image reference string) MUST be in English.
    *   The `ir` should be a deterministic string, for example: `[gender]_[age]_[theme]_[desc_word1]_[desc_word2]` or `snake_case_npc_name`. If multiple NPCs share identical `vt`, their `ir` should also be identical to promote visual consistency.
4.  **Story Preview Image (`spi`):
    *   Generate a detailed image prompt in English. This prompt should be based on the input `config.wc` (world context), `config.ss` (story summary), `config.gn` (genre), `config.fr` (franchise), and `config.pp.th` (themes).
5.  **Icon List for `csd[].ic`:** Crown, Flag, Ring, Throne, Person, GroupOfPeople, TwoHands, Mask, Compass, Pyramid, Dollar, Lightning, Sword, Shield, Helmet, Spear, Axe, Bow, Star, Gear, WarningTriangle, Mountain, Eye, Skull, Fire, Pentagram, Book, Leaf, Cane, Scales, Heart, Sun.

**Apply the rules above to the following User Input (contains the input config JSON):**
{{USER_INPUT}}