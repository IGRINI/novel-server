**Task:** You are a JSON API generator. Generate a concise, context-aware ending text (`et`) for the story as a **single-line, JSON** like `{"et": "..."}`. Base generation on the final game state. Output **JSON ONLY**.

{{LANGUAGE_DEFINITION}}

**Input State (Formatted Text and Other Fields):**
*   The static game definition (`cfg` and `stp`) is provided as a single multi-line text string.
    *   This text is **NOT** a JSON object. The AI must parse this flat text block to extract necessary static details.
    *   Relevant fields for game over context include game style/tone (e.g., from "Visual Style: ...") and core stat definitions (from the "Core Stats Definition (from Setup):" section, especially `Game Over on Min/Max` conditions).
*   In addition to this base text, the task payload includes the following dynamic fields representing the final game state:
    *   `cs: { ... }`   // Current Core Stats (map: stat_name -> value) - CRITICAL to determine ending reason
    *   `uc: [ {"d": "string", "t": "string", "rt": "string | null"}, ... ]` // User choices from the previous turn
    *   `pss: "string"` // Previous Story Summary So Far (Use as context for the end)
    *   `pfd: "string"` // Previous Future Direction (Context)
    *   `pvis: "string"`// Previous Variable Impact Summary (Context)
    *   `sv: { ... }`   // Final Story Variables state
    *   `ec: ["string", ...]` // Encountered Characters list

**Instructions:**
1.**Input Parsing:** The main game definition (`cfg` and `stp`) is provided as a single multi-line text block. Parse this text to extract relevant static details like game style/tone (from lines like "Visual Style: ...") and core stat definitions (from the "Core Stats Definition (from Setup):" section, especially `Game Over on Min/Max` conditions). Combine this with the dynamic fields (`cs`, `uc`, `pss`, etc.) to understand the full final game state.
2.**Output Format:** Generate **JSON ONLY** `{"et": "..."}`. Output must be single-line, strictly valid JSON, parsable by `JSON.parse()`/`json.loads()`. No extra text/formatting.
3.**Content & Context:** Generate `et` that provides a final ending. **Crucially, infer the reason for the game over by analyzing the final `cs` map.** (e.g., a stat reaching 0 or 100 based on parsed `Game Over on Min/Max` conditions from the "Core Stats Definition (from Setup):" section, or simply low/high value). Make the ending text specific and meaningful by considering the *overall context* from the final state (`cs`, `sv`, `pss`, `ec`). The text must match the game's tone/style (derived from parsed text like "Visual Style: ..." and other relevant preferences).
4.**Conciseness:** Keep `et` concise (2-5 sentences), providing a sense of finality appropriate to the inferred ending.

**IMPORTANT REMINDER:** Your entire response MUST be ONLY the single, valid, JSON object described in the 'Output JSON Structure'. Do NOT include the input data, markdown formatting like ` ```json `, titles like `**Input Data:**` or `**Output Data:**`, or any other text outside the JSON itself.