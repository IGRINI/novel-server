# 🎮 AI: Game Config JSON Generator (JSON API Mode)

**Task:** Generate a new game config as a single-line JSON based on `UserInput` string.

**Input (`UserInput`):** Simple string describing the desired game.

**Output JSON Adherence:**
Your ENTIRE response MUST be ONLY a single-line, valid JSON object. This JSON object MUST strictly adhere to the schema named 'generate_narrator_config' provided programmatically. Do NOT include any other text, markdown, or the input data in your response.

**Key Content Generation Instructions:**
1.  Use `UserInput` as the primary source for game description and details.
2.  **Core Stats (`cs`):** The `cs` field in the output JSON MUST contain EXACTLY 4 unique core stats. Define their names, descriptions (in System Prompt language), initial values (`iv` between 0-100), and game over (`go`) conditions.
3.  **Adult Content (`ac`):** Auto-determine the boolean `ac` flag based on the generated content and `UserInput`. Do not rely on user requests for this flag.
4.  **Player Name (`pn`):** Generate a specific `pn` (player_name) in System Prompt language. Avoid generic names like "Player" or "Hero" unless `UserInput` explicitly requests it.
5.  **Story Start (`sssf`, `fd`):** The `sssf` (story_summary_so_far) field should describe the very beginning of the story. The `fd` (future_direction) field should outline a plan for the first scene. Both should be in System Prompt language.
6.  **Language for Specific Fields:**
    *   `pp.st` (style for visual/narrative) and `pp.cvs` (character_visual_style image prompt) MUST be in English.
    *   All other textual content (titles, descriptions, genre, tone, player details, world context, story summaries, stat names/descriptions) should be in the System Prompt language.
7.  Ensure all required fields as per the 'generate_narrator_config' schema are present.

**Apply the rules above to the following UserInput:**
{{USER_INPUT}}