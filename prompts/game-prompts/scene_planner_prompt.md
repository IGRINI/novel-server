**Task:** AI Scene Planner. Analyze current game state and output a single JSON object with directives for the next scene.

All strings, except `pr` and `ir`, are output in `{{LANGUAGE_DEFINITION}}`; both of these fields are always in English.

# Objective:
Output a JSON with directives for the next scene, including new NPCs, potential new cards (implied by `ncds`), NPC updates/removals, and scene focus.

# Input:
A multi-line plain text describing the current game state and general game configuration.
The AI should parse this input text to extract values for general context (like `Title`, `Short Description`, `Genre`, `Protagonist Name`, `Protagonist Description`, `World Context`, `Story Summary`, `Adult Content`, `Player Preferences`, `Core Stats Definition`) and dynamic game data (like `Current Location`, `All NPCs` (including their detailed attributes), `Recent Events Summary`, `Protagonist ID`, `Current Scene Cards` (including their detailed attributes), and `Last Player Choices` (if present)).

# Instructions:
1.  Parse the input text to extract game state information and analyze all extracted fields.
2.  **New NPCs (`nnc` & `ncs`):**
    *   Set `nnc`: true if an NPC is CRITICAL for plot progression and existing NPCs cannot fulfill the role. Prioritize using/updating existing NPCs.
    *   If `nnc` is true, provide 1-2 `ncs` (each with `role`, `reason`). Otherwise, `ncs` is an empty array or omitted.
3.  **New Scene Cards (`ncds`):**
    *   Analyze `sf` and `current_scene_cards`.
    *   If new visual cards are CRITICAL to represent `sf` or key actions/elements, and suitable cards are not in `current_scene_cards`, provide 1-3 `ncds`. Prioritize existing cards. Each suggestion includes:
        *   `pr` (string): A short, precise visual description for the new card image. This description MUST be consistent with the overall application style: "A moody, high-contrast digital illustration with dark tones, soft neon accents, and a focused central composition blending fantasy and minimalism, using a palette of deep blues, teals, cyan glow, and occasional purples for atmosphere."
        *   `ir` (string): A unique and descriptive name or identifier for the card's generated image (e.g., 'abandoned_library_clue', 'city_marketplace_encounter'). This should be in snake_case and suitable for use as a filename or asset ID. It can be based on the card's title or its narrative purpose in the scene.
        *   `title` (string): Card title/caption.
        *   `reason` (string): Justification for the card.
    *   Otherwise, `ncds` should be an empty array or omitted.
4.  **NPC Updates (`cus`):** For NPCs affected by recent events:
    *   `id`: NPC's string ID.
    *   `mu` (optional): NPC's new complete memory, integrating recent events with existing memories.
    *   `ru` (optional): Patch object for relationships (keys are stringified character IDs, values are new descriptive strings).
    *   Include NPC in `cus` only if memory or relationships changed.
5.  **NPC Removal (`crs`):**
    *   List `crs` (e.g., irrelevant, plot purpose fulfilled) with `id` and `reason`.
6.  **Card Removal (`cdrs`):**
    *   List `cdrs` (e.g., outdated or irrelevant cards) with `ref_name` and `reason`.
7.  **Scene Focus (`sf`):**
    *   `sf`: 1-2 sentence narrative direction/objective for the next scene.

# Output JSON Structure:
Output ONLY a single, valid JSON object as described below.
```json
{
  "nnc": "boolean",
  "ncs": [
    {
      "role": "string",
      "reason": "string"
    }
  ],
  "ncds": [
    {
      "pr": "string",
      "ir": "string",
      "title": "string",
      "reason": "string"
    }
  ],
  "cus": [
    {
      "id": "string",
      "mu": "string",
      "ru": {
        "/* character_id */": "string"
      }
    }
  ],
  "crs": [
    {
      "id": "string",
      "reason": "string"
    }
  ],
  "cdrs": [
    {
      "ref_name": "string",
      "reason": "string"
    }
  ],
  "sf": "string"
}
```