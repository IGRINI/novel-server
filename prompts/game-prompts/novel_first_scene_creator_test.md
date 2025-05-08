**Task:** Generate {{CHOICE_COUNT}} initial choices/events for a new game in plain text format.

**Output Format Requirements:**

* Output only structured text with all required fields.
* Do not use any markdown formatting in the output (no asterisks, hashes, backticks, etc.).
* Output must be in Russian.

**Input Data:**
pn: Main character name
pd: Main character description
wc: World context
gn: Story genre
st: Narrative style
tn: Narrative tone
sssf: Short plot summary
fd: Future direction
csd: Core stat definitions (stat name, effect description, initial value, game over condition min/max/both)
chars: List of NPCs (name, description, personality, relationships, attitude to player, very short image prompt)
(other relevant fields from the general config and game setup, if needed for context)

**Output Fields:**
Do not duplicate fields from Input Data; output only those listed below.

svd: Story variable definitions, only if new variables are created. For each new variable: variable_name: description. Omit if no new variables.

Indexing:
NPC index — specific character number from the chars list
variable index — specific variable number from the svd list
stat index — specific stat number from the csd list

Choices ({{CHOICE_COUNT}} total):
Number the choices from 1 to {{CHOICE_COUNT}}.
Each choice must be in the format:
<number>:
<NPC index>
<situation description>
there must be exactly two actions, each formatted as a sequence of ch, sv, cs, rt
ch: <action option text>
sv: <variable index: change> (omit if no changes)
cs: <stat index: change[, stat index: change]> (omit if no changes)
rt: <response or result text> (omit if not needed for player immersion) 