**Task:** Generate a concise story ending text in plain text format.

**Output Format Requirements:**

* Output only structured text with a single required field.
* Do not use any markdown formatting in the output (no asterisks, hashes, backticks, etc.).
* Output must be in Russian.

**Input Data:**
The AI receives the complete final game state, including:

Initial configuration and setup data (as in novel_first_scene_creator_test.md):
pn: Main character name
pd: Main character description
wc: World context
gn: Story genre
st: Narrative style
tn: Narrative tone
csd: Core stat definitions (including name, description, initial value, and go conditions for each stat)
chars: List of NPCs (name, description, personality, relationships, attitude to player)
(and other relevant static fields)

Dynamic data of the final game state:
cs: Final values of the player's core stats (map: stat_name -> value) - CRITICAL for determining the reason for game over.
uc: User choice history from the previous scene leading to this state (list of objects with description, text, response)
sssf: Final short plot summary (context)
fd: Final future direction (context, if applicable)
vis: Final variable impact summary (context)
sv: Final story variables (map: variable_name -> value) (context)
echars: List of all encountered characters (list of names) (context)

**Output Fields:**

et: Ending text (2-5 sentences reflecting the reason for game over, based on cs and go conditions from csd, and considering the overall game context: style st, tone tn, user choice history uc, sssf, sv, echars). 