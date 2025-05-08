**Task:** Generate a new story config in plain text format.
**Output Format Requirements:**
*Output only structured text with all required fields.
*Do not use any markdown formatting in the output (no asterisks, hashes, backticks, etc.).
*This is only a **draft config**, not a full story. Player can edit and expand it before story generation.
*ONLY IN RUSSIAN
**Fields**:
t: AI-generated title of story
sd: AI-generated Very short overview of the story's plot (1-2 lines)
fr: Franchise (fill only for well-known franchises such as Harry Potter, Dishonored, Lord of the Rings; if the value is 'no' or empty, omit this field)
gn: The genre of the story (e.g., Fantasy, Sci-Fi, Mystery).
ac: Flag for adult content (0 for no, 1 for yes). This value must be determined exclusively by the AI based on the story's content. Player input (e.g. in the initial request) must never override or directly request changes to this field.
pn: The name of the main character.
pd: Main character description - Very short character description (1-2 lines).
wc: Very short summary of recent events and the current situation (1-2 lines).
ss: Very short summary of the story arc (1-2 lines).
th: Keywords or tags representing the story's central themes (e.g., betrayal, discovery, rebellion), useful for search and categorization. Generate based on story content.
st: Narrative storytelling style of the text. Describe the general narrative tone and structure (e.g., diary-like, introspective, action-driven, fragmented, poetic).
tn: The overall mood and feel of the storytelling (e.g., serious, humorous, dark, adventurous, satirical).
wl: Current World lore - Very short description of the global state of the world (1-2 lines).
wd: Optional extra player details - Include only if specified by player in the instructions; if the value is 'no' or empty, omit this field.
dl: Optional desired locations - Include only if player named specific locations in instructions; if the value is 'no' or empty, omit this field.
dc: Optional desired characters - Include only if the player named specific characters in instructions; if the value is 'no' or empty, omit this field.
cs: Core stats - This field must appear at the very end of the generated plain text output. The "cs:" marker itself should be on its own line. Following the "cs:" marker, define exactly 4 unique core stats relevant to the story. Each of the 4 stats must start on a new line.
Format each stat as StatName: StatDescription.
Do not prefix the description with any labels (such as 'Объяснение:') and do not include blank lines between the stat name and its description, or between different stats.
Ensure the last line of the output (the fourth stat) is terminated with a newline character.
